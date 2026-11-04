package httpapi

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Fel1xKan/mdserve/internal/auth"
	"github.com/Fel1xKan/mdserve/internal/site"
)

type testEnv struct {
	srv  *Server
	root string
	sc   *auth.Scope
}

func newTestEnv(t *testing.T, base, host string) *testEnv {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"README.md":        "# 首页\n\n欢迎。\n",
		"guide/README.md":  "# 指南\n\n总览。\n",
		"guide/install.md": "# 安装\n\n运行 mdserve serve。\n",
		"vendor/notes.md":  "# 用户目录\n\n这个目录名不能被内部路由遮住。\n",
		"secret.md":        "# 隐藏页\n\n只能直接访问。\n",
		"diagram.md":       "# 图\n\n```mermaid\ngraph TD; A-->B;\n```\n",
		"assets/logo.svg":  `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	reg := site.NewRegistry([]site.MountSpec{{Name: "docs", Host: host, Root: root}}, site.Options{
		Refresh:  time.Hour,
		Exclude:  []string{".git"},
		Hidden:   []string{"secret.md"},
		MaxBytes: 4 << 20,
		AllowExt: []string{".svg"},
	})

	a, err := auth.New(auth.Options{
		Secret:     []byte("0123456789abcdef0123456789abcdef"),
		Epoch:      "e1",
		CookieName: "mdserve_s",
		TTL:        8 * time.Hour,
		Idle:       30 * time.Minute,
		ShareTTL:   time.Hour,
		RateLimit:  auth.RateLimit{Attempts: 5, Window: time.Minute, Lockout: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	sc := a.NewScope(host, auth.Credential{Plain: "s3cret"}, auth.RateLimit{
		Attempts: 5, Window: time.Minute, Lockout: time.Minute,
	})

	srv, err := New(Options{
		Registry: reg,
		Auth:     auth.NewSet([]*auth.Scope{sc}),
		BasePath: base,
		Version:  "test-1.0",
		Repo:     "https://github.com/Fel1xKan/mdserve",
		Started:  time.Now(),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{srv: srv, root: root, sc: sc}
}

func (e *testEnv) do(t *testing.T, method, path, host string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if host != "" {
		r.Host = host
	}
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, r)
	return w
}

func (e *testEnv) login(t *testing.T, base string) []*http.Cookie {
	t.Helper()
	form := url.Values{"secret": {"s3cret"}, "next": {base + "/"}}
	w := e.do(t, http.MethodPost, base+"/-/login", "", form, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("登录状态码 = %d，body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("登录成功应写入 cookie")
	}
	return cookies
}

func TestHealthAndVendorArePublic(t *testing.T) {
	e := newTestEnv(t, "", "")

	w := e.do(t, http.MethodGet, "/-/health", "", nil, nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "ok" {
		t.Fatalf("health = %d %q", w.Code, w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/-/vendor/app.css", "", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("app.css = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("app.css Content-Type = %q", ct)
	}

	w = e.do(t, http.MethodGet, "/-/vendor/", "", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("vendor 目录列表应 404，得到 %d", w.Code)
	}
}

func TestUnauthenticatedRedirectsToLogin(t *testing.T) {
	e := newTestEnv(t, "", "")
	w := e.do(t, http.MethodGet, "/guide/install.md", "", nil, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("未登录状态码 = %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/-/login?next=") {
		t.Fatalf("Location = %q", loc)
	}
}

func TestLoginAndRender(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")

	w := e.do(t, http.MethodGet, "/guide/install.md", "", nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("文档状态码 = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "运行 mdserve serve") {
		t.Fatal("正文没有渲染")
	}
	if !strings.Contains(body, `id="安装"`) {
		t.Fatal("标题锚点缺失")
	}
	if !strings.Contains(body, "/vendor/app.css") {
		t.Fatal("样式表链接缺失")
	}
	if strings.Contains(body, "secret.md") {
		t.Fatal("隐藏文件不应出现在目录树里")
	}
}

func TestWrongPassword(t *testing.T) {
	e := newTestEnv(t, "", "")
	form := url.Values{"secret": {"nope"}, "next": {"/"}}
	w := e.do(t, http.MethodPost, "/-/login", "", form, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "不正确") {
		t.Fatal("应提示口令或分享码不正确")
	}
}

func TestShareCodeLogin(t *testing.T) {
	e := newTestEnv(t, "", "")
	code, _ := e.sc.ShareCode()

	form := url.Values{"secret": {code}, "next": {"/"}}
	w := e.do(t, http.MethodPost, "/-/login", "", form, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("分享码登录状态码 = %d，body=%s", w.Code, w.Body.String())
	}

	w = e.do(t, http.MethodPost, "/-/login", "", form, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("分享码重复使用应 401，得到 %d", w.Code)
	}
}

func TestBasePath(t *testing.T) {
	e := newTestEnv(t, "/notes", "")

	w := e.do(t, http.MethodGet, "/notes/guide/", "", nil, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("未登录状态码 = %d", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/notes/-/login") {
		t.Fatalf("Location = %q", loc)
	}

	if w := e.do(t, http.MethodGet, "/elsewhere", "", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("base 之外的路径应 404，得到 %d", w.Code)
	}

	if w := e.do(t, http.MethodGet, "/notes/-/vendor/app.css", "", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("base 下的静态资源 = %d", w.Code)
	}

	cookies := e.login(t, "/notes")
	w = e.do(t, http.MethodGet, "/notes/guide/", "", nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("登录后 = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `href="/notes/guide/install.md"`) {
		t.Fatal("目录树链接应带 base_path")
	}
	if !strings.Contains(body, `action="/notes/search"`) {
		t.Fatal("搜索表单应带 base_path")
	}
}

func TestUserVendorDirectoryIsNotShadowed(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")

	w := e.do(t, http.MethodGet, "/vendor/", "", nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("用户 vendor 目录 = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "notes.md") {
		t.Fatal("用户 vendor 目录应正常列出")
	}
}

func TestHiddenIsDirectlyReachable(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")

	w := e.do(t, http.MethodGet, "/secret.md", "", nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("hidden 文件直接访问 = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "只能直接访问") {
		t.Fatal("hidden 文件内容缺失")
	}
}

func TestSearch(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")

	w := e.do(t, http.MethodGet, "/search?q=%E5%AE%89%E8%A3%85", "", nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("检索状态码 = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "install.md") {
		t.Fatal("检索应命中 install.md")
	}
}

func TestUnknownHostGets421(t *testing.T) {
	e := newTestEnv(t, "", "docs.example.com")
	w := e.do(t, http.MethodGet, "/", "evil.example.com", nil, nil)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("未知 Host 状态码 = %d，想要 421", w.Code)
	}
}

func TestTreeTogglesAndShowsIndex(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")
	w := e.do(t, http.MethodGet, "/", "", nil, cookies)
	body := w.Body.String()

	// 目录行整行可点：role=button + aria-expanded，名字不再是链接。
	if !strings.Contains(body, `class="node-label" role="button" tabindex="0" aria-expanded=`) {
		t.Fatal("目录行应可点击（role=button + aria-expanded）")
	}
	if strings.Contains(body, `class="node-label" role="button" tabindex="0" aria-expanded="true"><a`) {
		t.Fatal("目录名不应再是链接，否则点击会跳页而不是展开")
	}
	// 有首页的目录把首页渲染成第一个子项「概述」，链接到目录地址。
	if !strings.Contains(body, `href="/guide/" title="guide/README.md">概述</a>`) {
		t.Fatalf("目录首页应渲染成子项「概述」：%s", body)
	}
}

func TestUnknownHostWarnsOnce(t *testing.T) {
	e := newTestEnv(t, "", "docs.example.com")
	var buf bytes.Buffer
	e.srv.log = slog.New(slog.NewTextHandler(&buf, nil))

	for i := 0; i < 3; i++ {
		if w := e.do(t, http.MethodGet, "/", "old.example.com", nil, nil); w.Code != http.StatusMisdirectedRequest {
			t.Fatalf("状态码 = %d，想要 421", w.Code)
		}
	}
	if n := strings.Count(buf.String(), "old.example.com"); n != 1 {
		t.Fatalf("同一个未知 Host 应只告警一次，实际 %d 次：\n%s", n, buf.String())
	}
}

func TestUnknownHostWarnLimit(t *testing.T) {
	e := newTestEnv(t, "", "docs.example.com")
	var buf bytes.Buffer
	e.srv.log = slog.New(slog.NewTextHandler(&buf, nil))

	for i := 0; i < unknownHostLogLimit+5; i++ {
		e.do(t, http.MethodGet, "/", fmt.Sprintf("h%d.example.com", i), nil, nil)
	}
	if n := strings.Count(buf.String(), "没有对应挂载点"); n != unknownHostLogLimit {
		t.Fatalf("告警条数 = %d，想要 %d", n, unknownHostLogLimit)
	}
}

func TestAboutIsPublic(t *testing.T) {
	e := newTestEnv(t, "", "")
	w := e.do(t, http.MethodGet, "/-/about", "", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("未登录 about = %d，想要 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "test-1.0") {
		t.Fatal("关于页应显示版本")
	}
}

func TestSVGIsAttachment(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")
	w := e.do(t, http.MethodGet, "/assets/logo.svg", "", nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("svg = %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("SVG 应强制下载，Content-Disposition = %q", cd)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("SVG 应带 sandbox CSP，得到 %q", csp)
	}
}

func TestNotFound(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")
	w := e.do(t, http.MethodGet, "/nope", "", nil, cookies)
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "404") {
		t.Fatal("应渲染 404 页面")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")
	w := e.do(t, http.MethodDelete, "/", "", nil, cookies)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，想要 405", w.Code)
	}
}

func TestMermaidFrameCSP(t *testing.T) {
	e := newTestEnv(t, "", "")
	w := e.do(t, http.MethodGet, "/-/mermaid", "", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", w.Code)
	}
	csp := w.Header().Get("Content-Security-Policy")
	// sandbox iframe 是 opaque origin，'self' 不匹配，必须写具体 origin。
	if !strings.Contains(csp, "script-src http://example.com") {
		t.Fatalf("CSP 应包含具体 origin，得到 %q", csp)
	}
	if strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatal("空壳页需要能被同源页面 iframe，frame-ancestors 不能是 none")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("X-Frame-Options 应被移除，得到 %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/vendor/mermaid.min.js") || !strings.Contains(body, "/vendor/mermaid-frame.js") {
		t.Fatal("空壳页应加载 mermaid 与 frame 脚本")
	}
}

func (e *testEnv) doEncoded(t *testing.T, path string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, r)
	return w
}

func TestVendorGzipAndETag(t *testing.T) {
	e := newTestEnv(t, "", "")

	w := e.doEncoded(t, "/-/vendor/app.css", nil, map[string]string{"Accept-Encoding": "gzip, deflate"})
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", w.Code)
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("Cache-Control = %q", cc)
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("解压: %v", err)
	}
	if !strings.Contains(string(plain), "--bg:") {
		t.Fatal("解压后的内容不是 app.css")
	}

	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("缺少 ETag")
	}
	w2 := e.doEncoded(t, "/-/vendor/app.css", nil, map[string]string{"If-None-Match": etag})
	if w2.Code != http.StatusNotModified {
		t.Fatalf("条件请求状态码 = %d，想要 304", w2.Code)
	}
	if w2.Body.Len() != 0 {
		t.Fatal("304 不应带 body")
	}
}

func TestVendorRejectsUnknownAndTraversal(t *testing.T) {
	e := newTestEnv(t, "", "")
	for _, p := range []string{"/-/vendor/nope.js", "/-/vendor/../main.go", "/-/vendor/"} {
		if w := e.do(t, http.MethodGet, p, "", nil, nil); w.Code != http.StatusNotFound {
			t.Fatalf("%s = %d，想要 404", p, w.Code)
		}
	}
}

func TestAssetVersionInHTML(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")
	w := e.do(t, http.MethodGet, "/", "", nil, cookies)
	body := w.Body.String()
	// 注意要连 -/ 前缀一起断言：少了它页面会去请求 /vendor/...，而路由在 /-/vendor/。
	if !strings.Contains(body, "/-/vendor/app.css?v=") {
		t.Fatal("样式表链接应指向 /-/vendor/ 并带版本号")
	}
	if !strings.Contains(body, "/-/vendor/app.js?v=") {
		t.Fatal("脚本链接应指向 /-/vendor/ 并带版本号")
	}
}

func TestHTMLGzip(t *testing.T) {
	e := newTestEnv(t, "", "")
	cookies := e.login(t, "")

	plain := e.do(t, http.MethodGet, "/guide/install.md", "", nil, cookies)
	if plain.Body.Len() < 1024 {
		t.Fatalf("页面太小（%d 字节），无法验证压缩", plain.Body.Len())
	}

	w := e.doEncoded(t, "/guide/install.md", cookies, map[string]string{"Accept-Encoding": "gzip"})
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", w.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("解压: %v", err)
	}
	if !strings.Contains(string(out), "运行 mdserve serve") {
		t.Fatal("压缩后的页面内容不对")
	}
}

func TestGzipQZeroIsRespected(t *testing.T) {
	e := newTestEnv(t, "", "")
	w := e.doEncoded(t, "/-/vendor/app.css", nil, map[string]string{"Accept-Encoding": "gzip;q=0"})
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("q=0 时不该压缩，得到 %q", got)
	}
}
