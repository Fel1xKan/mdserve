package auth

import (
	"net/http"
	"strings"
	"time"
)

// Scope 是"某个 host + 它的口令 + 它的限流器"的组合。
// 会话 token 里绑定了 host，所以 A 站的 cookie 拿到 B 站一定失败。
type Scope struct {
	auth    *Auth
	host    string
	cred    Credential
	limiter *limiter
}

// NewScope 为 host 建一个 Scope。host 为空表示默认站点。
func (a *Auth) NewScope(host string, cred Credential, rl RateLimit) *Scope {
	return &Scope{
		auth:    a,
		host:    strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), ".")),
		cred:    cred,
		limiter: newLimiter(rl.Attempts, rl.Window, rl.Lockout),
	}
}

// Host 返回该 scope 绑定的域名。
func (s *Scope) Host() string { return s.host }

// Credential 返回口令来源，供启动横幅展示。
func (s *Scope) Credential() Credential { return s.cred }

// CheckPassword 校验口令。
func (s *Scope) CheckPassword(pw string) bool { return s.cred.Verify(pw) }

// Issue 签发本 scope 的会话 token。
func (s *Scope) Issue() (string, time.Time) { return s.auth.Issue(s.host) }

// Valid 校验 token 是否属于本 scope。
func (s *Scope) Valid(token string) bool { return s.auth.Valid(s.host, token) }

// Authed 判断请求是否带着本 scope 的有效会话。
func (s *Scope) Authed(r *http.Request) bool {
	c, err := r.Cookie(s.auth.CookieName())
	if err != nil {
		return false
	}
	return s.Valid(c.Value)
}

// SetCookie 写入会话 cookie。
func (s *Scope) SetCookie(w http.ResponseWriter, r *http.Request, token string, exp time.Time) {
	s.auth.SetCookie(w, r, token, exp)
}

// ClearCookie 删除会话 cookie。
func (s *Scope) ClearCookie(w http.ResponseWriter, r *http.Request) {
	s.auth.ClearCookie(w, r)
}

// Renew 在剩余寿命不足一半时续签。
func (s *Scope) Renew(w http.ResponseWriter, r *http.Request, token string) {
	s.auth.Renew(w, r, s.host, token)
}

// ShareCode 生成本 scope 的一次性分享码。
func (s *Scope) ShareCode() (string, time.Time) { return s.auth.IssueShare(s.host) }

// RedeemShare 消费分享码；成功返回 true，且同一个码只能用一次。
func (s *Scope) RedeemShare(code string) bool { return s.auth.RedeemShare(s.host, code) }

// Allow 判断某个 IP 现在是否可以尝试口令。
func (s *Scope) Allow(ip string) (bool, time.Duration) { return s.limiter.allow(ip) }

// Fail 记一次失败。
func (s *Scope) Fail(ip string) { s.limiter.fail(ip) }

// Reset 清掉某个 IP 的失败记录。
func (s *Scope) Reset(ip string) { s.limiter.reset(ip) }

// Set 按 host 索引所有 Scope。
type Set struct {
	byHost map[string]*Scope
	def    *Scope
	all    []*Scope
}

// NewSet 用已建好的 Scope 构造索引。最多一个 host 为空的默认站点。
func NewSet(scopes []*Scope) *Set {
	s := &Set{byHost: make(map[string]*Scope, len(scopes))}
	for _, sc := range scopes {
		if sc == nil {
			continue
		}
		s.all = append(s.all, sc)
		if sc.host == "" {
			if s.def == nil {
				s.def = sc
			}
			continue
		}
		s.byHost[sc.host] = sc
	}
	if s.def == nil && len(s.all) == 1 {
		s.def = s.all[0]
	}
	return s
}

// For 按 Host 找 Scope。未匹配时回落到默认站点；没有默认站点则返回 false，
// 调用方应回 421。
func (s *Set) For(host string) (*Scope, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if sc, ok := s.byHost[host]; ok {
		return sc, true
	}
	if s.def != nil {
		return s.def, true
	}
	return nil, false
}

// Default 返回默认站点，可能为 nil。
func (s *Set) Default() *Scope { return s.def }

// All 返回全部 Scope，按配置顺序。
func (s *Set) All() []*Scope { return s.all }
