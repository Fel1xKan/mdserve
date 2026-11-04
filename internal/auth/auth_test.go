package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestAuth(t *testing.T) (*Auth, *clock) {
	t.Helper()
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	a, err := New(Options{
		Secret:     []byte("0123456789abcdef0123456789abcdef"),
		Epoch:      "epoch-1",
		CookieName: "mdserve_s",
		TTL:        8 * time.Hour,
		Idle:       30 * time.Minute,
		ShareTTL:   time.Hour,
		RateLimit:  RateLimit{Attempts: 3, Window: time.Minute, Lockout: time.Minute},
		Now:        c.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, c
}

func TestTokenRoundTrip(t *testing.T) {
	a, c := newTestAuth(t)

	tok, exp := a.Issue("docs.example.com")
	if !a.Valid("docs.example.com", tok) {
		t.Fatal("刚签发的 token 应该有效")
	}
	if a.Valid("other.example.com", tok) {
		t.Fatal("token 绑定 host，换 host 必须失败")
	}
	if !exp.After(c.t) {
		t.Fatal("过期时间应在未来")
	}

	c.t = c.t.Add(9 * time.Hour)
	if a.Valid("docs.example.com", tok) {
		t.Fatal("过期 token 必须失败")
	}
}

func TestTokenRejectsTampering(t *testing.T) {
	a, _ := newTestAuth(t)
	tok, _ := a.Issue("docs.example.com")

	parts := strings.Split(tok, ".")
	if len(parts) != 4 {
		t.Fatalf("token 段数 = %d", len(parts))
	}
	parts[1] = "9999999999" // 改过期时间，签名应失配
	if a.Valid("docs.example.com", strings.Join(parts, ".")) {
		t.Fatal("篡改过期时间后必须失败")
	}
	if a.Valid("docs.example.com", "garbage") {
		t.Fatal("乱码必须失败")
	}
	if a.Valid("docs.example.com", "") {
		t.Fatal("空 token 必须失败")
	}
}

func TestShareCodeIsOneTime(t *testing.T) {
	a, c := newTestAuth(t)

	code, _ := a.IssueShare("docs.example.com")
	if !a.RedeemShare("docs.example.com", code) {
		t.Fatal("首次兑换应成功")
	}
	if a.RedeemShare("docs.example.com", code) {
		t.Fatal("同一个分享码不能兑换第二次")
	}

	code2, _ := a.IssueShare("docs.example.com")
	if a.RedeemShare("other.example.com", code2) {
		t.Fatal("分享码绑定 host，换 host 必须失败")
	}

	code3, _ := a.IssueShare("docs.example.com")
	c.t = c.t.Add(2 * time.Hour)
	if a.RedeemShare("docs.example.com", code3) {
		t.Fatal("过期分享码必须失败")
	}
}

func TestEpochRotationInvalidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "epoch")
	if _, err := RotateEpoch(path); err != nil {
		t.Fatal(err)
	}
	epoch, err := LoadEpoch(path)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	a, err := New(Options{
		Secret:     []byte("0123456789abcdef0123456789abcdef"),
		Epoch:      epoch,
		EpochPath:  path,
		CookieName: "mdserve_s",
		TTL:        time.Hour,
		Idle:       10 * time.Minute,
		ShareTTL:   time.Hour,
		Now:        c.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := a.Issue("docs.example.com")
	if !a.Valid("docs.example.com", tok) {
		t.Fatal("初始 token 应有效")
	}

	if _, err := RotateEpoch(path); err != nil {
		t.Fatal(err)
	}
	// Epoch() 有 2 秒缓存，推进时钟让它重读文件。
	c.t = c.t.Add(3 * time.Second)
	if a.Valid("docs.example.com", tok) {
		t.Fatal("轮换 epoch 后旧 token 必须失效")
	}
}

func TestCredentialArgon2id(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("哈希前缀 = %q", hash)
	}
	cred := Credential{Hash: hash}
	if !cred.Verify("correct horse battery") {
		t.Fatal("正确口令应通过")
	}
	if cred.Verify("wrong") {
		t.Fatal("错误口令必须失败")
	}
	if cred.Verify("") {
		t.Fatal("空口令必须失败")
	}
	if err := validateHash("sha256:abc"); err == nil {
		t.Fatal("sha256 哈希必须被拒绝")
	}
}

func TestResolveCredentialGeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()
	cred, err := ResolveCredential("", "", dir, "abcd")
	if err != nil {
		t.Fatal(err)
	}
	if !cred.Generated || len(cred.Plain) != 20 {
		t.Fatalf("应生成 20 位口令，实际 %+v", cred)
	}
	st, err := os.Stat(cred.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); permEnforced && perm != 0o600 {
		t.Fatalf("生成文件权限 = %04o，想要 0600", perm)
	}

	again, err := ResolveCredential("", "", dir, "abcd")
	if err != nil {
		t.Fatal(err)
	}
	if again.Generated {
		t.Fatal("第二次应复用文件，而不是重新生成")
	}
	if again.Plain != cred.Plain {
		t.Fatal("复用的口令应与首次一致")
	}
}

func TestResolveCredentialRejectsLoosePerms(t *testing.T) {
	if !permEnforced {
		t.Skip("Windows 用 ACL 控制访问，没有 Unix 权限位可检查")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "pass")
	if err := os.WriteFile(p, []byte("hunter2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCredential("", p, dir, "k"); err == nil {
		t.Fatal("其他用户可读的口令文件应被拒绝")
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	cred, err := ResolveCredential("", p, dir, "k")
	if err != nil {
		t.Fatal(err)
	}
	if !cred.Verify("hunter2") {
		t.Fatal("口令文件里的明文应能校验")
	}
}

func TestLimiterLocksAndResets(t *testing.T) {
	a, _ := newTestAuth(t)
	sc := a.NewScope("docs.example.com", Credential{Plain: "pw"}, RateLimit{
		Attempts: 3, Window: time.Minute, Lockout: time.Minute,
	})
	ip := "203.0.113.9"
	for i := 0; i < 3; i++ {
		if ok, _ := sc.Allow(ip); !ok {
			t.Fatalf("第 %d 次不该被锁", i+1)
		}
		sc.Fail(ip)
	}
	if ok, wait := sc.Allow(ip); ok || wait <= 0 {
		t.Fatalf("连续失败后应被锁，ok=%v wait=%v", ok, wait)
	}
	sc.Reset(ip)
	if ok, _ := sc.Allow(ip); !ok {
		t.Fatal("Reset 后应放行")
	}
}

func TestClientIPOnlyTrustsLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("CF-Connecting-IP", "198.51.100.7")
	if got := ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("回环来源应采信 CF-Connecting-IP，得到 %q", got)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "192.0.2.5:5555"
	r2.Header.Set("CF-Connecting-IP", "198.51.100.7")
	if got := ClientIP(r2); got != "192.0.2.5" {
		t.Fatalf("非回环来源不该采信头，得到 %q", got)
	}
}

func TestRequestHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "Docs.Example.COM:8443"
	if got := RequestHost(r); got != "docs.example.com" {
		t.Fatalf("RequestHost = %q", got)
	}
}
