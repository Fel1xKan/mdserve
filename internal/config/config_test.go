package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate 把 HOME 与 XDG_CONFIG_HOME 指到临时目录，
// 避免测试读到开发机上真实的 ~/.config/mdserve/config.yaml。
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, k := range []string{
		"MDSERVE_LISTEN", "MDSERVE_PUBLIC_URL", "MDSERVE_BASE_PATH",
		"MDSERVE_ALLOW_EXT", "MDSERVE_EXCLUDE", "MDSERVE_HIDDEN",
		"MDSERVE_LOG_LEVEL", "MDSERVE_LOG_FORMAT", "MDSERVE_TUNNEL_MODE",
		"MDSERVE_REFRESH", "MDSERVE_MAX_FILE_BYTES",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	return home
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "mdserve.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPriority(t *testing.T) {
	dir := isolate(t)
	root := t.TempDir()
	cfgPath := writeConfig(t, dir, `
version: 1
server:
  listen: 127.0.0.1:9001
  log_level: warn
  allow_ext: [".abc"]
mounts:
  - name: docs
    root: `+root+`
`)

	t.Setenv("MDSERVE_LISTEN", "127.0.0.1:9002")
	t.Setenv("MDSERVE_LOG_LEVEL", "error")

	eff, err := Load(Options{ConfigPath: cfgPath, Listen: "127.0.0.1:9003"})
	if err != nil {
		t.Fatal(err)
	}
	if got := eff.Config.Server.Listen; got != "127.0.0.1:9003" {
		t.Fatalf("listen = %q，想要 flag 覆盖", got)
	}
	if got := eff.Source("server.listen"); got != "flag" {
		t.Fatalf("listen 来源 = %q", got)
	}
	if got := eff.Config.Server.LogLevel; got != "error" {
		t.Fatalf("log_level = %q，想要 env 覆盖 file", got)
	}
	// 列表追加：默认值 + 文件里的 .abc
	found := false
	for _, e := range eff.Config.Server.AllowExt {
		if e == ".abc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("allow_ext 应包含文件里的 .abc，实际 %v", eff.Config.Server.AllowExt)
	}
	if len(eff.Config.Server.AllowExt) <= 1 {
		t.Fatalf("allow_ext 应保留默认值并追加，实际 %v", eff.Config.Server.AllowExt)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	dir := isolate(t)
	cfgPath := writeConfig(t, dir, `
server:
  listne: 127.0.0.1:9001
`)
	if _, err := Load(Options{ConfigPath: cfgPath, SkipRootCheck: true}); err == nil {
		t.Fatal("拼错的键名应该报错")
	}
}

func TestDefaultMountIsRoot(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	eff, err := Load(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(eff.Config.Mounts) != 1 {
		t.Fatalf("挂载点数量 = %d", len(eff.Config.Mounts))
	}
	if eff.Config.Mounts[0].Root != root {
		t.Fatalf("root = %q，想要 %q", eff.Config.Mounts[0].Root, root)
	}
	if eff.Config.Mounts[0].Host != "" {
		t.Fatalf("默认挂载点 host 应为空，实际 %q", eff.Config.Mounts[0].Host)
	}
}

func TestSkipRootCheck(t *testing.T) {
	isolate(t)
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := Load(Options{Root: missing}); err == nil {
		t.Fatal("root 不存在时 serve 应该报错")
	}
	if _, err := Load(Options{Root: missing, SkipRootCheck: true}); err != nil {
		t.Fatalf("SkipRootCheck 时不该检查 root: %v", err)
	}
}

func TestBasePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"/", ""},
		{"notes", "/notes"},
		{"/notes/", "/notes"},
		{"/a/b/", "/a/b"},
	}
	for _, c := range cases {
		got, err := NormalizeBasePath(c.in)
		if err != nil {
			t.Fatalf("NormalizeBasePath(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("NormalizeBasePath(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"/-/x", "/a/../b", "/a?b", "/a b"} {
		if _, err := NormalizeBasePath(bad); err == nil {
			t.Fatalf("NormalizeBasePath(%q) 应该报错", bad)
		}
	}
}

func TestTunnelModes(t *testing.T) {
	isolate(t)
	root := t.TempDir()

	_, err := Load(Options{Root: root, TunnelMode: "auto"})
	if err == nil || !strings.Contains(err.Error(), "auto") {
		t.Fatalf("未知模式应被拒绝，得到 %v", err)
	}

	_, err = Load(Options{Root: root, TunnelMode: "named", TunnelHost: "docs.example.com"})
	if err == nil {
		t.Fatal("named 模式缺少 token/config 应报错")
	}

	tokenFile := filepath.Join(root, "token")
	if err := os.WriteFile(tokenFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	eff, err := Load(Options{
		Root: root, TunnelMode: "named", TunnelHost: "docs.example.com", TunnelToken: tokenFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Config.Tunnel.Mode != "named" {
		t.Fatalf("mode = %q", eff.Config.Tunnel.Mode)
	}
}

func TestHostNormalization(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	eff, err := Load(Options{
		Root:          root,
		SkipRootCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Config.Mounts[0].Host != "" {
		t.Fatal("默认站点 host 应为空")
	}

	cfgPath := writeConfig(t, t.TempDir(), `
mounts:
  - name: a
    host: Docs.Example.COM.
    root: `+root+`
  - name: b
    host: docs.example.com
    root: `+root+`
`)
	if _, err := Load(Options{ConfigPath: cfgPath}); err == nil {
		t.Fatal("大小写/尾点归一化后重复的 host 应报错")
	}
}

func TestDurationsAndValidation(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if _, err := Load(Options{Root: root, SkipRootCheck: false, TunnelMode: ""}); err != nil {
		t.Fatal(err)
	}

	eff, err := Load(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Config.Auth.CookieTTL.Duration != 8*time.Hour {
		t.Fatalf("cookie_ttl 默认值 = %v", eff.Config.Auth.CookieTTL.Duration)
	}
	if eff.Config.Auth.ShareTTL.Duration != time.Hour {
		t.Fatalf("share_ttl 默认值 = %v", eff.Config.Auth.ShareTTL.Duration)
	}
}

func TestRedacted(t *testing.T) {
	cfg := Default()
	cfg.Mounts = []Mount{{
		Name: "docs",
		Root: "/tmp/docs",
		Auth: &MountAuth{PasswordHash: "$argon2id$secret", PasswordFile: "/etc/pass"},
	}}
	out := cfg.Redacted()
	if out.Mounts[0].Auth.PasswordHash != "***" {
		t.Fatal("password_hash 应被遮蔽")
	}
	if cfg.Mounts[0].Auth.PasswordHash == "***" {
		t.Fatal("Redacted 不应修改原对象")
	}
}
