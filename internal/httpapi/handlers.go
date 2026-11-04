package httpapi

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Fel1xKan/mdserve/internal/auth"
	"github.com/Fel1xKan/mdserve/internal/render"
	"github.com/Fel1xKan/mdserve/internal/site"
	"github.com/Fel1xKan/mdserve/web"
)

// handleHealth 是唯一完全公开的端点：只回 "ok"，不含版本、主机名或路径，
// 便于隧道/容器探活而不泄露任何信息。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleVendor 提供内置静态资源（CSS/JS/字体）。这些资源不含用户内容，
// 所以不需要鉴权；但目录列表一律 404。
func (s *Server) handleVendor(w http.ResponseWriter, r *http.Request, p string) {
	if !allowGet(w, r) {
		return
	}
	name := strings.TrimPrefix(p, pathVendor)
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		s.renderNotFound(w, r)
		return
	}
	a, ok := web.Lookup(name)
	if !ok {
		s.renderNotFound(w, r)
		return
	}

	etag := `"` + a.ETag + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Vary", "Accept-Encoding")
	// 内容哈希变了 ?v= 就变，所以可以放心让浏览器与边缘缓存一年。
	h.Set("Cache-Control", "public, max-age=31536000, immutable")

	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	h.Set("Content-Type", a.Type)
	if acceptsGzip(r) {
		if gz := a.Gzip(); gz != nil {
			h.Set("Content-Encoding", "gzip")
			h.Set("Content-Length", strconv.Itoa(len(gz)))
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(gz)
			}
			return
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(a.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(a.Data)
	}
}

// handleMermaid 返回渲染 mermaid 的空壳页。它被放进 sandbox="allow-scripts"
// 的 iframe 里，文档内容通过 postMessage 传入，所以这个页面本身不含数据。
//
// 注意 CSP：sandbox 让文档变成 opaque origin，`'self'` 不再匹配任何地址，
// 必须把具体 origin 写进 script-src。
func (s *Server) handleMermaid(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	origin := s.origin(r)
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "mermaid_frame", map[string]string{
		"Base":   s.base,
		"Origin": origin,
		"Asset":  web.AssetVersion(),
	}); err != nil {
		s.log.Error("模板渲染失败", "template", "mermaid_frame", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src "+origin+"; style-src 'unsafe-inline'; "+
			"img-src data:; font-src data:; connect-src 'none'; frame-ancestors 'self'")
	writeHTML(w, r, http.StatusOK, buf.Bytes())
}

// handleAbout 需要登录。刻意只显示版本信息，不暴露根目录、配置路径、
// 主机名等本地细节。
func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	_, sc, ok := s.mountFor(r)
	if !ok {
		s.renderStatus(w, r, http.StatusMisdirectedRequest, "notfound", view{Title: "421", Path: r.URL.Path})
		return
	}
	if !sc.Authed(r) {
		s.redirect(w, r, s.loginURL(requestURI(r)), http.StatusSeeOther)
		return
	}
	s.render(w, r, "about", view{
		Title:  "关于",
		Commit: shortCommit(s.opt.Commit),
		Date:   s.opt.Date,
		Uptime: humanUptime(time.Since(s.opt.Started)),
	})
}

// handleLogin 处理登录表单。同一个输入框既接受口令，也接受一次性分享码：
// 先按口令校验，失败且看起来像分享码（s1. 前缀）再尝试兑换。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	_, sc, ok := s.mountFor(r)
	if !ok {
		s.renderStatus(w, r, http.StatusMisdirectedRequest, "notfound", view{Title: "421", Path: r.URL.Path})
		return
	}
	next := s.safeNext(r.URL.Query().Get("next"))

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if sc.Authed(r) {
			s.redirect(w, r, next, http.StatusSeeOther)
			return
		}
		s.render(w, r, "login", view{Title: "登录", Next: next})
	case http.MethodPost:
		// 限制请求体，且必须在读之前。
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			s.renderStatus(w, r, http.StatusBadRequest, "login", view{
				Title: "登录", Next: next, Error: "请求格式不正确",
			})
			return
		}
		next = s.safeNext(r.PostFormValue("next"))
		ip := auth.ClientIP(r)

		if allowed, wait := sc.Allow(ip); !allowed {
			secs := int(wait.Seconds()) + 1
			s.renderStatus(w, r, http.StatusTooManyRequests, "login", view{
				Title: "登录", Next: next,
				Error: fmt.Sprintf("尝试过于频繁，请 %d 秒后再试", secs),
			})
			return
		}

		secret := r.PostFormValue("secret")
		byPassword := sc.CheckPassword(secret)
		byShare := false
		if !byPassword && strings.HasPrefix(secret, "s1.") {
			byShare = sc.RedeemShare(secret)
		}
		if !byPassword && !byShare {
			sc.Fail(ip)
			s.renderStatus(w, r, http.StatusUnauthorized, "login", view{
				Title: "登录", Next: next, Error: "口令或分享码不正确",
			})
			return
		}

		sc.Reset(ip)
		token, exp := sc.Issue()
		sc.SetCookie(w, r, token, exp)
		s.redirect(w, r, next, http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLogout 清掉会话 cookie。只接受 POST：SameSite=Lax 会让跨站 POST
// 不带 cookie，因此不存在"被强制登出"的 CSRF。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, sc, ok := s.mountFor(r)
	if !ok {
		s.renderStatus(w, r, http.StatusMisdirectedRequest, "notfound", view{Title: "421", Path: r.URL.Path})
		return
	}
	sc.ClearCookie(w, r)
	s.redirect(w, r, s.loginURL(""), http.StatusSeeOther)
}

// handleSearch 渲染检索结果页。
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	m, sc, ok := s.mountFor(r)
	if !ok {
		s.renderStatus(w, r, http.StatusMisdirectedRequest, "notfound", view{Title: "421", Path: r.URL.Path})
		return
	}
	if !sc.Authed(r) {
		s.redirect(w, r, s.loginURL(requestURI(r)), http.StatusSeeOther)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var results []site.SearchResult
	if q != "" {
		results = m.Search(q, 100)
	}
	s.render(w, r, "search", view{
		Title:     "检索",
		SiteLabel: m.Name,
		BrandSub:  hostSub(m),
		Query:     q,
		Count:     len(results),
		Results:   results,
	})
}

// handleDoc 是主路由：文档、目录、静态资源都从这里进。
func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request, p string) {
	if !allowGet(w, r) {
		return
	}
	m, sc, ok := s.mountFor(r)
	if !ok {
		s.renderStatus(w, r, http.StatusMisdirectedRequest, "notfound", view{Title: "421", Path: r.URL.Path})
		return
	}
	if !sc.Authed(r) {
		s.redirect(w, r, s.loginURL(requestURI(r)), http.StatusSeeOther)
		return
	}

	rel := strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
	res := m.Resolve(rel)

	switch res.Kind {
	case site.KindDoc:
		// /guide 命中 guide/README.md 时补尾斜杠，否则相对链接会错层。
		if res.Dir != "" && !strings.HasSuffix(p, "/") {
			s.redirect(w, r, s.base+p+"/", http.StatusMovedPermanently)
			return
		}
		doc, err := m.Doc(res.Path)
		if err != nil {
			s.renderNotFound(w, r)
			return
		}
		s.renderDoc(w, r, m, doc, p)
	case site.KindDir:
		if !strings.HasSuffix(p, "/") {
			s.redirect(w, r, s.base+p+"/", http.StatusMovedPermanently)
			return
		}
		// 根目录没有首页、也没有任何文档时给欢迎页，而不是空列表。
		if res.Dir == "" && m.Count() == 0 {
			s.render(w, r, "landing", view{Title: productName, SiteLabel: m.Name})
			return
		}
		s.renderListing(w, r, m, res.Dir)
	case site.KindAsset:
		s.serveAsset(w, r, m, res.Path)
	default:
		s.renderNotFound(w, r)
	}
}

func (s *Server) renderDoc(w http.ResponseWriter, r *http.Request, m *site.Mount, doc *site.Doc, p string) {
	title := doc.Title
	if title == "" {
		title = m.Name
	}
	s.render(w, r, "layout", view{
		Title:      title,
		SiteLabel:  m.Name,
		BrandSub:   hostSub(m),
		Path:       strings.TrimPrefix(p, "/"),
		Body:       doc.HTML,
		TOCHTML:    render.TOC(doc.Headings),
		TreeHTML:   treeHTML(m.Tree(), doc.Path, s.base),
		HasMermaid: doc.HasMermaid,
	})
}

func (s *Server) renderListing(w http.ResponseWriter, r *http.Request, m *site.Mount, dir string) {
	title := "文档目录"
	if dir != "" {
		title = path.Base(dir)
	}
	s.render(w, r, "layout", view{
		Title:     title,
		SiteLabel: m.Name,
		BrandSub:  hostSub(m),
		Path:      dir,
		Body:      listingHTML(dir, m.Entries(dir), s.base),
		TreeHTML:  treeHTML(m.Tree(), dir, s.base),
	})
}

// serveAsset 直出白名单内的静态文件。SVG 一律强制下载并加 sandbox CSP，
// 避免同源脚本执行。
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, m *site.Mount, rel string) {
	abs, err := m.AssetPath(rel)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}
	// private：允许浏览器缓存，但不允许 Cloudflare 之类的共享缓存留存，
	// 否则受保护资源可能被缓存后发给未登录的人。
	w.Header().Set("Cache-Control", "private, max-age=300")
	if strings.EqualFold(path.Ext(rel), ".svg") {
		name := strings.NewReplacer(`"`, "", "\\", "", "\r", "", "\n", "").Replace(path.Base(rel))
		if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
			w.Header().Set("Content-Disposition", v)
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	http.ServeFile(w, r, abs)
}

func (s *Server) renderNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderStatus(w, r, http.StatusNotFound, "notfound", view{
		Title: "404",
		Path:  r.URL.Path,
	})
}

// safeNext 只允许跳回本站路径，挡掉 //evil.com 与反斜杠变体，
// 并且必须落在 base_path 之内。
func (s *Server) safeNext(next string) string {
	root := s.base + "/"
	if next == "" || !strings.HasPrefix(next, "/") {
		return root
	}
	if strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return root
	}
	if s.base != "" && next != s.base && !strings.HasPrefix(next, s.base+"/") {
		return root
	}
	return next
}

// origin 返回当前请求的外部 origin，用于给 opaque-origin 的 iframe 写 CSP。
func (s *Server) origin(r *http.Request) string {
	if s.opt.PublicURL != "" {
		return strings.TrimSuffix(s.opt.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func hostSub(m *site.Mount) string {
	if m.Host != "" {
		return m.Host
	}
	return "本地文档"
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	if c == "" {
		return "dev"
	}
	return c
}

func repoLabel(repo string) string {
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")
	if repo == "" {
		return "GitHub"
	}
	return repo
}

func humanUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%d 分 %d 秒", mins, int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
}
