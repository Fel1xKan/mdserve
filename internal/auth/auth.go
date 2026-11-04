package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const tokenVersion = "v1"

// RateLimit 是口令失败的限制参数。
type RateLimit struct {
	Attempts int
	Window   time.Duration
	Lockout  time.Duration
}

// Options 是 New 的入参。Secret 与 Epoch 由调用方从文件读出，
// 这样 auth 包不关心文件布局。
type Options struct {
	Secret     []byte
	Epoch      string
	EpochPath  string
	CookieName string
	CookiePath string
	TTL        time.Duration
	Idle       time.Duration
	ShareTTL   time.Duration
	Secure     bool
	RateLimit  RateLimit
	Now        func() time.Time
}

// Auth 负责签名、验签与 cookie。它不持有任何口令：
// 口令校验在 Scope 上，因为每个 host 的口令不同。
type Auth struct {
	secret     []byte
	epoch      string
	epochPath  string
	epochMu    sync.Mutex
	epochAt    time.Time
	cookieName string
	cookiePath string
	ttl        time.Duration
	idle       time.Duration
	shareTTL   time.Duration
	secure     bool
	used       *usedSet
	now        func() time.Time
}

// New 校验参数并构造 Auth。
func New(opts Options) (*Auth, error) {
	if len(opts.Secret) < 16 {
		return nil, errors.New("auth: secret 至少 16 字节")
	}
	if opts.CookieName == "" {
		return nil, errors.New("auth: cookie_name 不能为空")
	}
	if opts.TTL <= 0 || opts.Idle <= 0 {
		return nil, errors.New("auth: cookie_ttl 与 idle_ttl 必须为正")
	}
	if opts.Idle > opts.TTL {
		return nil, errors.New("auth: idle_ttl 不能大于 cookie_ttl")
	}
	if opts.ShareTTL <= 0 || opts.ShareTTL > 24*time.Hour {
		return nil, errors.New("auth: share_ttl 需在 1m 到 24h 之间")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	path := opts.CookiePath
	if path == "" {
		path = "/"
	}
	return &Auth{
		secret:     opts.Secret,
		epoch:      opts.Epoch,
		epochPath:  opts.EpochPath,
		cookieName: opts.CookieName,
		cookiePath: path,
		ttl:        opts.TTL,
		idle:       opts.Idle,
		shareTTL:   opts.ShareTTL,
		secure:     opts.Secure,
		used:       newUsedSet(4096),
		now:        now,
	}, nil
}

// Epoch 返回当前 epoch。配置了 epochPath 时每 2 秒最多重读一次，
// 这样 `mdserve revoke` 不需要重启服务就能生效。
func (a *Auth) Epoch() string {
	if a.epochPath == "" {
		return a.epoch
	}
	a.epochMu.Lock()
	defer a.epochMu.Unlock()
	if !a.epochAt.IsZero() && a.now().Sub(a.epochAt) < 2*time.Second {
		return a.epoch
	}
	a.epochAt = a.now()
	if b, err := os.ReadFile(a.epochPath); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			a.epoch = v
		}
	}
	return a.epoch
}

// CookieName 供处理器判断是否已登录。
func (a *Auth) CookieName() string { return a.cookieName }

// TTL 是会话绝对有效期。
func (a *Auth) TTL() time.Duration { return a.ttl }

// mac 用 0 分隔各段，避免 "a|b" 与 "ab|" 之类的拼接歧义。
func (a *Auth) mac(parts ...string) []byte {
	h := hmac.New(sha256.New, a.secret)
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

func (a *Auth) sign(kind, host, payload string) string {
	sum := a.mac(kind, host, a.Epoch(), payload)
	return base64.RawURLEncoding.EncodeToString(sum)
}

// Issue 签发一个会话 token。格式：v1.<exp>.<nonce>.<mac>。
func (a *Auth) Issue(host string) (string, time.Time) {
	exp := a.now().Add(a.ttl)
	payload := tokenVersion + "." + strconv.FormatInt(exp.Unix(), 10) + "." + rand.Text()
	return payload + "." + a.sign("sess", host, payload), exp
}

// Valid 校验 token 的格式、过期时间与签名。host 参与签名，
// 所以 A 站的 cookie 拿到 B 站一定失败。
func (a *Auth) Valid(host, token string) bool {
	if token == "" || len(token) > 512 {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != tokenVersion {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || a.now().Unix() >= exp {
		return false
	}
	payload := parts[0] + "." + parts[1] + "." + parts[2]
	return hmac.Equal([]byte(a.sign("sess", host, payload)), []byte(parts[3]))
}

// Authed 判断请求是否带着有效会话。
func (a *Auth) Authed(r *http.Request) bool {
	c, err := r.Cookie(a.cookieName)
	if err != nil {
		return false
	}
	return a.Valid(RequestHost(r), c.Value)
}

// SetCookie 写入会话 cookie。Path 取 base_path，避免子路径部署时
// cookie 被发到站点之外。
func (a *Auth) SetCookie(w http.ResponseWriter, r *http.Request, token string, exp time.Time) {
	c := &http.Cookie{
		Name:     a.cookieName,
		Value:    token,
		Path:     a.cookiePath,
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
		HttpOnly: true,
		Secure:   a.secure || isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	}
	if c.MaxAge < 0 {
		c.MaxAge = 0
	}
	http.SetCookie(w, c)
}

// ClearCookie 删除会话 cookie。
func (a *Auth) ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    "",
		Path:     a.cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secure || isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// Renew 在剩余寿命不足一半时续签，返回新 token（未续签时为空）。
func (a *Auth) Renew(w http.ResponseWriter, r *http.Request, host, token string) {
	c, err := r.Cookie(a.cookieName)
	if err != nil || c.Value != token {
		return
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	if time.Until(time.Unix(exp, 0)) > a.idle/2 {
		return
	}
	fresh, next := a.Issue(host)
	a.SetCookie(w, r, fresh, next)
}

// RequestHost 返回用于路由与签名的小写域名（不含端口）。
func RequestHost(r *http.Request) string {
	h := r.Host
	if h == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}

// ClientIP 只在直连方是回环时采信 CF-Connecting-IP：
// cloudflared 从 127.0.0.1 连过来，其它来源一律用 RemoteAddr。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
			if net.ParseIP(v) != nil {
				return v
			}
		}
	}
	return host
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
