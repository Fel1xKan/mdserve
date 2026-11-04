package httpapi

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Fel1xKan/mdserve/internal/auth"
	"github.com/Fel1xKan/mdserve/internal/site"
	"github.com/Fel1xKan/mdserve/web"
)

const productName = "mdserve"

// 内部端点统一挂在 /-/ 下，把 /vendor、/search、/healthz 这些名字让给用户目录。
// 这样用户有一个叫 vendor 的目录时不会被服务遮住。
const (
	pathHealth  = "/-/health"
	pathAbout   = "/-/about"
	pathLogin   = "/-/login"
	pathLogout  = "/-/logout"
	pathSearch  = "/search"
	pathVendor  = "/-/vendor/"
	pathMermaid = "/-/mermaid"
)

// Options 是构造 Server 的入参。
type Options struct {
	Registry *site.Registry
	Auth     *auth.Set
	// PublicURL 用于生成绝对链接；留空时按请求推断。
	PublicURL string
	// BasePath 是站点挂载的子路径，空表示根路径。
	BasePath string
	Version  string
	Commit   string
	Date     string
	Repo     string
	Started  time.Time
}

// Server 是 HTTP 入口。它只做路由、鉴权与模板渲染，
// 内容解析全部委托给 site。
type Server struct {
	reg  *site.Registry
	auth *auth.Set
	tpl  *template.Template
	opt  Options
	base string
	log  *slog.Logger

	// 未知 Host 的告警去重。Host 头由请求方控制，不设上限会把日志冲掉。
	unknownMu sync.Mutex
	unknown   map[string]struct{}
}

// New 解析模板并准备静态资源处理器。
func New(opt Options, log *slog.Logger) (*Server, error) {
	tpl, err := template.New("mdserve").ParseFS(web.FS, "*.html")
	if err != nil {
		return nil, fmt.Errorf("解析模板: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		reg:  opt.Registry,
		auth: opt.Auth,
		tpl:  tpl,
		opt:  opt,
		base: opt.BasePath,
		log:  log,
	}, nil
}

// ServeHTTP 是唯一入口。顺序很重要：先剥 base_path，再分派内部端点，
// 最后才进文档路由，避免用户目录与内部端点重名。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.setSecurityHeaders(w)
	p, ok := s.stripBase(w, r)
	if !ok {
		return
	}

	switch {
	case p == pathHealth:
		s.handleHealth(w, r)
	case p == pathMermaid:
		s.handleMermaid(w, r)
	case strings.HasPrefix(p, pathVendor):
		s.handleVendor(w, r, p)
	case p == pathAbout:
		s.handleAbout(w, r)
	case p == pathLogin:
		s.handleLogin(w, r)
	case p == pathLogout:
		s.handleLogout(w, r)
	case strings.HasPrefix(p, "/-/"):
		s.renderNotFound(w, r)
	case p == pathSearch:
		s.handleSearch(w, r)
	default:
		s.handleDoc(w, r, p)
	}
}

// stripBase 把 base_path 从请求路径里去掉。挂在 /notes 下时，
// /notes、/notes/、/notes/guide 分别变成 301、/、/guide。
func (s *Server) stripBase(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := r.URL.Path
	if s.base == "" {
		return p, true
	}
	if p == s.base {
		u := *r.URL
		u.Path = s.base + "/"
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
		return "", false
	}
	if !strings.HasPrefix(p, s.base+"/") {
		s.renderNotFound(w, r)
		return "", false
	}
	return p[len(s.base):], true
}

func (s *Server) setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Content-Security-Policy",
		"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; font-src 'self'; connect-src 'self'; "+
			"frame-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

// mountFor 按 Host 找挂载点与对应的鉴权 scope。
// 找不到时返回 false，调用方应回 421。
func (s *Server) mountFor(r *http.Request) (*site.Mount, *auth.Scope, bool) {
	host := auth.RequestHost(r)
	m := s.reg.Lookup(host)
	if m == nil {
		s.noteUnknownHost(host)
		return nil, nil, false
	}
	sc, ok := s.auth.For(host)
	if !ok {
		return nil, nil, false
	}
	return m, sc, true
}

// unknownHostLogLimit 是未知 Host 告警的条数上限。
const unknownHostLogLimit = 16

// noteUnknownHost 为每个未知 Host 记一次警告。它多半意味着用户改过域名，
// 而 Cloudflare 上的旧记录还在把流量送过来；不重复记，也不回显给访客。
func (s *Server) noteUnknownHost(host string) {
	if host == "" {
		return
	}
	s.unknownMu.Lock()
	defer s.unknownMu.Unlock()
	if s.unknown == nil {
		s.unknown = make(map[string]struct{}, unknownHostLogLimit)
	}
	if _, ok := s.unknown[host]; ok {
		return
	}
	if len(s.unknown) >= unknownHostLogLimit {
		return
	}
	s.unknown[host] = struct{}{}
	s.log.Warn("请求的 Host 没有对应挂载点",
		"host", host,
		"hint", "如果这是改掉的旧域名，记得删除 Cloudflare 的 DNS 记录与隧道 ingress")
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, v view) {
	s.renderStatus(w, r, http.StatusOK, name, v)
}

// renderStatus 统一填公共字段，并在写 body 前设置好状态码。
// 页面先渲染到内存再决定是否压缩：文档页通常能压掉 60% 以上，
// 而模板输出远小于 1MB，缓冲的代价可以忽略。
func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, code int, name string, v view) {
	v.Base = s.base
	v.Product = productName
	v.Version = s.opt.Version
	v.Asset = web.AssetVersion()
	v.Repo = s.opt.Repo
	v.RepoLabel = repoLabel(s.opt.Repo)

	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, name, v); err != nil {
		s.log.Error("模板渲染失败", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeHTML(w, r, code, buf.Bytes())
}

// redirect 只允许站内跳转，并保留 base_path。
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to string, code int) {
	http.Redirect(w, r, to, code)
}

func (s *Server) loginURL(next string) string {
	u := s.base + pathLogin
	if next != "" && next != "/" {
		u += "?next=" + url.QueryEscape(next)
	}
	return u
}

// requestURI 返回带查询串的完整路径，用于登录后跳回。
func requestURI(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.RawQuery
}

// writeHTML 输出 HTML 页面。超过 1KB 且客户端接受 gzip 时压缩；
// 页面带 no-store，不参与任何缓存。
func writeHTML(w http.ResponseWriter, r *http.Request, code int, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Vary", "Accept-Encoding")

	if len(body) >= 1024 && acceptsGzip(r) {
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
		if err == nil {
			if _, werr := zw.Write(body); werr == nil && zw.Close() == nil {
				h.Set("Content-Encoding", "gzip")
				h.Set("Content-Length", strconv.Itoa(buf.Len()))
				w.WriteHeader(code)
				if r.Method != http.MethodHead {
					_, _ = w.Write(buf.Bytes())
				}
				return
			}
		}
	}

	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// acceptsGzip 解析 Accept-Encoding，正确处理 q=0。
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q := 1.0
		if params != "" {
			if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		return q > 0
	}
	return false
}

// etagMatch 判断 If-None-Match 是否命中。支持 *、列表与 W/ 前缀。
func etagMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == etag {
			return true
		}
	}
	return false
}

func allowGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}
