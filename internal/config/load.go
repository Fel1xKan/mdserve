package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Options 是命令行参数。零值表示"没给"，不覆盖配置。
type Options struct {
	ConfigPath    string
	Root          string
	Listen        string
	Port          int
	PublicURL     string
	BasePath      string
	PasswordFile  string
	TunnelMode    string
	TunnelToken   string
	TunnelConfig  string
	TunnelHost    string
	AllowExt      []string
	Exclude       []string
	Hidden        []string
	LogLevel      string
	LogFormat     string
	SkipRootCheck bool
}

// Effective 是加载结果：最终配置 + 实际读到的文件 + 每个字段的来源。
type Effective struct {
	Config  Config
	File    string
	Sources map[string]string
}

// Source 返回某个字段的来源：default / file / env / flag。
func (e *Effective) Source(key string) string {
	if s, ok := e.Sources[key]; ok {
		return s
	}
	return "default"
}

// Load 按 默认值 -> YAML -> 环境变量 -> 命令行 的顺序合并。
func Load(opts Options) (*Effective, error) {
	eff := &Effective{Config: Default(), Sources: make(map[string]string)}
	src := func(key, from string) { eff.Sources[key] = from }

	file, err := locate(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	eff.File = file
	if file != "" {
		if err := applyFile(&eff.Config, file, src); err != nil {
			return nil, err
		}
	}
	if err := applyEnv(&eff.Config, src); err != nil {
		return nil, err
	}
	if err := applyFlags(&eff.Config, opts, src); err != nil {
		return nil, err
	}
	if err := finalize(&eff.Config, opts, src); err != nil {
		return nil, err
	}
	eff.Config.Server.AllowExt = Normalize(eff.Config.Server.AllowExt)
	eff.Config.Server.Exclude = dedupe(eff.Config.Server.Exclude)
	eff.Config.Server.Hidden = dedupe(eff.Config.Server.Hidden)
	return eff, nil
}

// locate 找到要读的配置文件。显式指定的路径必须存在；
// 隐式查找时找不到就返回空串（全部用默认值）。
func locate(explicit string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("配置文件 %s: %w", abs, err)
		}
		return abs, nil
	}
	candidates := []string{"mdserve.yaml", "mdserve.yml", ".mdserve.yaml"}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		candidates = append(candidates, filepath.Join(dir, "mdserve", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "mdserve", "config.yaml"))
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			return abs, nil
		}
	}
	return "", nil
}

// applyFile 把 YAML 合并进 cfg。用 map 判断"键是否出现"，
// 从而区分"没写"与"写了零值"；再用 KnownFields 拒绝拼错的键名。
func applyFile(cfg *Config, path string, src func(string, string)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var fc Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&fc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if fc.Version != 0 && fc.Version != CurrentVersion {
		return fmt.Errorf("%s: 配置版本 %d 不受支持（当前 %d）", path, fc.Version, CurrentVersion)
	}
	if present(raw, "version") {
		cfg.Version = fc.Version
		src("version", "file")
	}

	s := &fc.Server
	if present(raw, "server", "listen") {
		cfg.Server.Listen = s.Listen
		src("server.listen", "file")
	}
	if present(raw, "server", "public_url") {
		cfg.Server.PublicURL = s.PublicURL
		src("server.public_url", "file")
	}
	if present(raw, "server", "base_path") {
		cfg.Server.BasePath = s.BasePath
		src("server.base_path", "file")
	}
	if present(raw, "server", "refresh") {
		cfg.Server.Refresh = s.Refresh
		src("server.refresh", "file")
	}
	if present(raw, "server", "max_file_bytes") {
		cfg.Server.MaxFileBytes = s.MaxFileBytes
		src("server.max_file_bytes", "file")
	}
	if present(raw, "server", "allow_ext") {
		cfg.Server.AllowExt = append(cfg.Server.AllowExt, s.AllowExt...)
		src("server.allow_ext", "file")
	}
	if present(raw, "server", "exclude") {
		cfg.Server.Exclude = append(cfg.Server.Exclude, s.Exclude...)
		src("server.exclude", "file")
	}
	if present(raw, "server", "hidden") {
		cfg.Server.Hidden = append(cfg.Server.Hidden, s.Hidden...)
		src("server.hidden", "file")
	}
	if present(raw, "server", "language") {
		cfg.Server.Language = s.Language
		src("server.language", "file")
	}
	if present(raw, "server", "log_level") {
		cfg.Server.LogLevel = s.LogLevel
		src("server.log_level", "file")
	}
	if present(raw, "server", "log_format") {
		cfg.Server.LogFormat = s.LogFormat
		src("server.log_format", "file")
	}

	a := &fc.Auth
	if present(raw, "auth", "secret_file") {
		cfg.Auth.SecretFile = a.SecretFile
		src("auth.secret_file", "file")
	}
	if present(raw, "auth", "state_dir") {
		cfg.Auth.StateDir = a.StateDir
		src("auth.state_dir", "file")
	}
	if present(raw, "auth", "cookie_name") {
		cfg.Auth.CookieName = a.CookieName
		src("auth.cookie_name", "file")
	}
	if present(raw, "auth", "cookie_ttl") {
		cfg.Auth.CookieTTL = a.CookieTTL
		src("auth.cookie_ttl", "file")
	}
	if present(raw, "auth", "idle_ttl") {
		cfg.Auth.IdleTTL = a.IdleTTL
		src("auth.idle_ttl", "file")
	}
	if present(raw, "auth", "share_ttl") {
		cfg.Auth.ShareTTL = a.ShareTTL
		src("auth.share_ttl", "file")
	}
	if present(raw, "auth", "rate_limit", "attempts") {
		cfg.Auth.RateLimit.Attempts = a.RateLimit.Attempts
		src("auth.rate_limit.attempts", "file")
	}
	if present(raw, "auth", "rate_limit", "window") {
		cfg.Auth.RateLimit.Window = a.RateLimit.Window
		src("auth.rate_limit.window", "file")
	}
	if present(raw, "auth", "rate_limit", "lockout") {
		cfg.Auth.RateLimit.Lockout = a.RateLimit.Lockout
		src("auth.rate_limit.lockout", "file")
	}

	t := &fc.Tunnel
	if present(raw, "tunnel", "provider") {
		cfg.Tunnel.Provider = t.Provider
		src("tunnel.provider", "file")
	}
	if present(raw, "tunnel", "mode") {
		cfg.Tunnel.Mode = t.Mode
		src("tunnel.mode", "file")
	}
	if present(raw, "tunnel", "binary") {
		cfg.Tunnel.Binary = t.Binary
		src("tunnel.binary", "file")
	}
	if present(raw, "tunnel", "token_file") {
		cfg.Tunnel.TokenFile = t.TokenFile
		src("tunnel.token_file", "file")
	}
	if present(raw, "tunnel", "config") {
		cfg.Tunnel.ConfigFile = t.ConfigFile
		src("tunnel.config", "file")
	}
	if present(raw, "tunnel", "hostname") {
		cfg.Tunnel.Hostname = t.Hostname
		src("tunnel.hostname", "file")
	}

	if present(raw, "mounts") {
		cfg.Mounts = fc.Mounts
		src("mounts", "file")
	}
	return nil
}

// present 判断嵌套键是否真的出现在 YAML 里。
func present(raw map[string]any, path ...string) bool {
	var cur any = raw
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		v, ok := m[key]
		if !ok {
			return false
		}
		cur = v
	}
	return true
}

// applyEnv 只读白名单里的变量；口令不在白名单里。
func applyEnv(cfg *Config, src func(string, string)) error {
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
			src(envKey(key), "env")
		}
	}
	list := func(key string, dst *[]string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = append(*dst, splitList(v)...)
			src(envKey(key), "env")
		}
	}
	dur := func(key string, dst *Duration) error {
		v, ok := os.LookupEnv(key)
		if !ok {
			return nil
		}
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", key, v, err)
		}
		dst.Duration = parsed
		src(envKey(key), "env")
		return nil
	}
	num := func(key string, dst *int64) error {
		v, ok := os.LookupEnv(key)
		if !ok {
			return nil
		}
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("%s=%q: 需要整数", key, v)
		}
		*dst = parsed
		src(envKey(key), "env")
		return nil
	}

	str("MDSERVE_LISTEN", &cfg.Server.Listen)
	str("MDSERVE_PUBLIC_URL", &cfg.Server.PublicURL)
	str("MDSERVE_BASE_PATH", &cfg.Server.BasePath)
	list("MDSERVE_ALLOW_EXT", &cfg.Server.AllowExt)
	list("MDSERVE_EXCLUDE", &cfg.Server.Exclude)
	list("MDSERVE_HIDDEN", &cfg.Server.Hidden)
	str("MDSERVE_LANGUAGE", &cfg.Server.Language)
	str("MDSERVE_LOG_LEVEL", &cfg.Server.LogLevel)
	str("MDSERVE_LOG_FORMAT", &cfg.Server.LogFormat)
	str("MDSERVE_SECRET_FILE", &cfg.Auth.SecretFile)
	str("MDSERVE_STATE_DIR", &cfg.Auth.StateDir)
	str("MDSERVE_COOKIE_NAME", &cfg.Auth.CookieName)
	str("MDSERVE_TUNNEL_MODE", &cfg.Tunnel.Mode)
	str("MDSERVE_TUNNEL_BINARY", &cfg.Tunnel.Binary)
	str("MDSERVE_TUNNEL_TOKEN_FILE", &cfg.Tunnel.TokenFile)
	str("MDSERVE_TUNNEL_CONFIG", &cfg.Tunnel.ConfigFile)
	str("MDSERVE_TUNNEL_HOSTNAME", &cfg.Tunnel.Hostname)

	for _, f := range []struct {
		key string
		dst *Duration
	}{
		{"MDSERVE_REFRESH", &cfg.Server.Refresh},
		{"MDSERVE_COOKIE_TTL", &cfg.Auth.CookieTTL},
		{"MDSERVE_IDLE_TTL", &cfg.Auth.IdleTTL},
		{"MDSERVE_SHARE_TTL", &cfg.Auth.ShareTTL},
		{"MDSERVE_RATE_WINDOW", &cfg.Auth.RateLimit.Window},
		{"MDSERVE_RATE_LOCKOUT", &cfg.Auth.RateLimit.Lockout},
	} {
		if err := dur(f.key, f.dst); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		key string
		dst *int64
	}{
		{"MDSERVE_MAX_FILE_BYTES", &cfg.Server.MaxFileBytes},
	} {
		if err := num(f.key, f.dst); err != nil {
			return err
		}
	}
	if v, ok := os.LookupEnv("MDSERVE_RATE_ATTEMPTS"); ok {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("MDSERVE_RATE_ATTEMPTS=%q: 需要整数", v)
		}
		cfg.Auth.RateLimit.Attempts = parsed
		src("auth.rate_limit.attempts", "env")
	}
	return nil
}

func envKey(name string) string {
	name = strings.TrimPrefix(name, "MDSERVE_")
	return strings.ToLower(strings.ReplaceAll(name, "_", "."))
}

// applyFlags 把命令行参数合并进来。参数优先级最高。
func applyFlags(cfg *Config, opts Options, src func(string, string)) error {
	if opts.Listen != "" {
		cfg.Server.Listen = opts.Listen
		src("server.listen", "flag")
	}
	if opts.Port != 0 {
		host := cfg.Server.Listen
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		if host == "" {
			host = "127.0.0.1"
		}
		cfg.Server.Listen = host + ":" + strconv.Itoa(opts.Port)
		src("server.listen", "flag")
	}
	if opts.PublicURL != "" {
		cfg.Server.PublicURL = opts.PublicURL
		src("server.public_url", "flag")
	}
	if opts.BasePath != "" {
		cfg.Server.BasePath = opts.BasePath
		src("server.base_path", "flag")
	}
	if opts.TunnelMode != "" {
		cfg.Tunnel.Mode = opts.TunnelMode
		src("tunnel.mode", "flag")
	}
	if opts.TunnelToken != "" {
		cfg.Tunnel.TokenFile = opts.TunnelToken
		src("tunnel.token_file", "flag")
	}
	if opts.TunnelConfig != "" {
		cfg.Tunnel.ConfigFile = opts.TunnelConfig
		src("tunnel.config", "flag")
	}
	if opts.TunnelHost != "" {
		cfg.Tunnel.Hostname = opts.TunnelHost
		src("tunnel.hostname", "flag")
	}
	if len(opts.AllowExt) > 0 {
		cfg.Server.AllowExt = append(cfg.Server.AllowExt, opts.AllowExt...)
		src("server.allow_ext", "flag")
	}
	if len(opts.Exclude) > 0 {
		cfg.Server.Exclude = append(cfg.Server.Exclude, opts.Exclude...)
		src("server.exclude", "flag")
	}
	if len(opts.Hidden) > 0 {
		cfg.Server.Hidden = append(cfg.Server.Hidden, opts.Hidden...)
		src("server.hidden", "flag")
	}
	if opts.LogLevel != "" {
		cfg.Server.LogLevel = opts.LogLevel
		src("server.log_level", "flag")
	}
	if opts.LogFormat != "" {
		cfg.Server.LogFormat = opts.LogFormat
		src("server.log_format", "flag")
	}
	return nil
}

// finalize 做展开、归一化与跨字段校验。到这里所有来源已经合并完。
func finalize(cfg *Config, opts Options, src func(string, string)) error {
	cfg.Auth.SecretFile = expandHome(cfg.Auth.SecretFile)
	cfg.Auth.StateDir = expandHome(cfg.Auth.StateDir)
	cfg.Tunnel.TokenFile = expandHome(cfg.Tunnel.TokenFile)
	cfg.Tunnel.ConfigFile = expandHome(cfg.Tunnel.ConfigFile)

	base, err := NormalizeBasePath(cfg.Server.BasePath)
	if err != nil {
		return err
	}
	cfg.Server.BasePath = base

	cfg.Server.LogLevel = strings.ToLower(strings.TrimSpace(cfg.Server.LogLevel))
	switch cfg.Server.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("server.log_level: 只支持 debug/info/warn/error，收到 %q", cfg.Server.LogLevel)
	}
	cfg.Server.LogFormat = strings.ToLower(strings.TrimSpace(cfg.Server.LogFormat))
	switch cfg.Server.LogFormat {
	case "text", "json":
	default:
		return fmt.Errorf("server.log_format: 只支持 text/json，收到 %q", cfg.Server.LogFormat)
	}
	cfg.Server.Language = strings.ToLower(strings.TrimSpace(cfg.Server.Language))
	if cfg.Server.Language == "" {
		cfg.Server.Language = "zh"
	}

	if cfg.Server.Refresh.Duration < time.Second {
		return fmt.Errorf("server.refresh: 不能小于 1s")
	}
	if cfg.Server.MaxFileBytes < 1<<10 {
		return fmt.Errorf("server.max_file_bytes: 不能小于 1024")
	}
	if cfg.Auth.CookieTTL.Duration < 5*time.Minute {
		return fmt.Errorf("auth.cookie_ttl: 不能小于 5m")
	}
	if cfg.Auth.IdleTTL.Duration < time.Minute {
		return fmt.Errorf("auth.idle_ttl: 不能小于 1m")
	}
	if cfg.Auth.IdleTTL.Duration > cfg.Auth.CookieTTL.Duration {
		return fmt.Errorf("auth.idle_ttl: 不能大于 auth.cookie_ttl")
	}
	if cfg.Auth.ShareTTL.Duration < time.Minute || cfg.Auth.ShareTTL.Duration > 24*time.Hour {
		return fmt.Errorf("auth.share_ttl: 需在 1m 到 24h 之间")
	}
	if cfg.Auth.RateLimit.Attempts < 3 {
		return fmt.Errorf("auth.rate_limit.attempts: 不能小于 3")
	}
	if cfg.Auth.RateLimit.Window.Duration < time.Minute {
		return fmt.Errorf("auth.rate_limit.window: 不能小于 1m")
	}
	if cfg.Auth.RateLimit.Lockout.Duration < time.Minute {
		return fmt.Errorf("auth.rate_limit.lockout: 不能小于 1m")
	}
	if cfg.Auth.CookieName == "" {
		return fmt.Errorf("auth.cookie_name: 不能为空")
	}

	if len(cfg.Mounts) == 0 {
		root := opts.Root
		if root == "" {
			root, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		cfg.Mounts = []Mount{{Root: root}}
		src("mounts", "default")
	}

	hosts := make(map[string]string, len(cfg.Mounts))
	names := make(map[string]int, len(cfg.Mounts))
	for i := range cfg.Mounts {
		m := &cfg.Mounts[i]
		if strings.TrimSpace(m.Root) == "" {
			return fmt.Errorf("mounts[%d]: root 不能为空", i)
		}
		abs, err := filepath.Abs(expandHome(m.Root))
		if err != nil {
			return fmt.Errorf("mounts[%d]: %w", i, err)
		}
		m.Root = filepath.Clean(abs)
		if strings.TrimSpace(m.Name) == "" {
			m.Name = filepath.Base(m.Root)
		}
		host, err := normalizeHost(m.Host)
		if err != nil {
			return fmt.Errorf("mounts[%d] (%s): %w", i, m.Name, err)
		}
		m.Host = host
		if prev, ok := hosts[host]; ok {
			return fmt.Errorf("mounts: host %q 重复（%s 与 %s）", host, prev, m.Name)
		}
		hosts[host] = m.Name
		names[m.Name]++
		if names[m.Name] > 1 {
			m.Name = m.Name + "-" + strconv.Itoa(i+1)
		}
		if m.Auth == nil && opts.PasswordFile != "" {
			m.Auth = &MountAuth{PasswordFile: opts.PasswordFile}
		}
		if m.Auth != nil {
			m.Auth.PasswordFile = expandHome(strings.TrimSpace(m.Auth.PasswordFile))
			m.Auth.PasswordHash = strings.TrimSpace(m.Auth.PasswordHash)
			if m.Auth.PasswordFile == "" && m.Auth.PasswordHash == "" {
				m.Auth = nil
			}
		}
		if !opts.SkipRootCheck {
			st, err := os.Stat(m.Root)
			if err != nil {
				return fmt.Errorf("mounts[%d] (%s): %w", i, m.Name, err)
			}
			if !st.IsDir() {
				return fmt.Errorf("mounts[%d] (%s): %s 不是目录", i, m.Name, m.Root)
			}
		}
	}
	if n := countEmptyHost(cfg.Mounts); n > 1 {
		return fmt.Errorf("mounts: 最多只能有一个 host 为空的默认站点，当前有 %d 个", n)
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Tunnel.Mode)) {
	case "", "none":
		cfg.Tunnel.Mode = "none"
	case "named":
		cfg.Tunnel.Mode = "named"
		if cfg.Tunnel.TokenFile == "" && cfg.Tunnel.ConfigFile == "" {
			return fmt.Errorf("tunnel.mode=named 需要 token_file 或 config")
		}
	default:
		return fmt.Errorf("tunnel.mode: 只支持 none/named，收到 %q", cfg.Tunnel.Mode)
	}
	if cfg.Tunnel.Mode == "named" && cfg.Tunnel.Hostname == "" {
		return fmt.Errorf("tunnel.mode=named 需要 hostname")
	}
	cfg.Tunnel.Provider = "cloudflared"
	cfg.Tunnel.Binary = strings.TrimSpace(cfg.Tunnel.Binary)
	if cfg.Tunnel.Binary == "" {
		cfg.Tunnel.Binary = "cloudflared"
	}
	return nil
}

func countEmptyHost(mounts []Mount) int {
	n := 0
	for _, m := range mounts {
		if m.Host == "" {
			n++
		}
	}
	return n
}

// normalizeHost 只保留裸域名：小写、去端口、去尾点，拒绝协议与路径。
func normalizeHost(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	if h == "" {
		return "", nil
	}
	if strings.Contains(h, "://") {
		return "", fmt.Errorf("host 不要带协议：%q", raw)
	}
	if strings.ContainsAny(h, "/\\?#@ ") {
		return "", fmt.Errorf("host 含非法字符：%q", raw)
	}
	h = strings.TrimSuffix(h, ".")
	if i := strings.LastIndex(h, ":"); i >= 0 {
		if strings.Contains(h[i+1:], "]") {
			return "", fmt.Errorf("host 不支持 IPv6 字面量：%q", raw)
		}
		h = h[:i]
	}
	if h == "" {
		return "", fmt.Errorf("host 为空：%q", raw)
	}
	return h, nil
}

// expandHome 只展开开头的 ~，其余交给 filepath。
func expandHome(p string) string {
	if p == "" {
		return ""
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
