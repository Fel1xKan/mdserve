// mdserve 把一个 markdown 目录渲染成需要口令的网站，并可选接入 Cloudflare named 隧道。
//
// 默认只绑回环、只读，不下载任何东西。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Fel1xKan/mdserve/internal/auth"
	"github.com/Fel1xKan/mdserve/internal/buildinfo"
	"github.com/Fel1xKan/mdserve/internal/config"
	"github.com/Fel1xKan/mdserve/internal/httpapi"
	"github.com/Fel1xKan/mdserve/internal/site"
	"github.com/Fel1xKan/mdserve/internal/tunnel"
	"github.com/Fel1xKan/mdserve/internal/uninstall"
	"github.com/Fel1xKan/mdserve/web"

	"gopkg.in/yaml.v3"
)

const repoURL = "https://github.com/Fel1xKan/mdserve"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mdserve:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve":
		return cmdServe(args)
	case "version":
		return cmdVersion(args)
	case "config":
		return cmdConfig(args)
	case "passwd":
		return cmdPasswd(args)
	case "share":
		return cmdShare(args)
	case "revoke":
		return cmdRevoke(args)
	case "uninstall", "clean":
		return cmdUninstall(args)
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("未知命令 %q；可用：serve version config passwd share revoke uninstall", cmd)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `mdserve — 把 markdown 目录变成需要口令的网站

用法：
  mdserve serve      [参数]   启动服务
  mdserve share      --host H 生成一次性分享码
  mdserve revoke              让所有会话与分享码立即失效
  mdserve passwd              生成随机口令与 argon2id 哈希
  mdserve config     [参数]   打印生效配置与来源
  mdserve version    [--json]
  mdserve uninstall  [--yes] [--purge]

常用参数（serve）：
  --root DIR            文档根目录，默认当前目录
  --listen ADDR         监听地址，默认 127.0.0.1:8787
  --port N              只改端口
  --base-path /notes    挂在域名子路径下
  --public-url URL      对外地址（非回环监听时必填）
  --password-file FILE  全局口令文件（0600）
  --tunnel none|named   隧道模式，named 需要 --tunnel-token-file 或 --tunnel-config
  --tunnel-host H       隧道域名
  --allow-ext .pdf      追加可直出的后缀（可重复）
  --exclude NAME        追加不扫描的目录名（可重复）
  --hidden GLOB         追加不展示但仍可访问的路径（可重复）
  --log-level L         debug|info|warn|error
  --log-format F        text|json

配置优先级：命令行 > 环境变量 > YAML > 默认值。
仓库：%s
`, repoURL)
}

// cmdFlags 是所有子命令共用的参数集合。
type cmdFlags struct {
	config.Options
	allowExtFlag []string
	excludeFlag  []string
	hiddenFlag   []string

	yes         bool
	purge       bool
	keepSecrets bool
	json        bool
	host        string
	ttl         time.Duration
	length      int
}

func (f *cmdFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.ConfigPath, "config", "", "配置文件路径")
	fs.StringVar(&f.Root, "root", "", "文档根目录")
	fs.StringVar(&f.Listen, "listen", "", "监听地址")
	fs.IntVar(&f.Port, "port", 0, "监听端口")
	fs.StringVar(&f.PublicURL, "public-url", "", "对外访问地址")
	fs.StringVar(&f.BasePath, "base-path", "", "子路径部署前缀")
	fs.StringVar(&f.PasswordFile, "password-file", "", "全局口令文件")
	fs.StringVar(&f.TunnelMode, "tunnel", "", "隧道模式 none|named")
	fs.StringVar(&f.TunnelToken, "tunnel-token-file", "", "隧道 token 文件")
	fs.StringVar(&f.TunnelConfig, "tunnel-config", "", "cloudflared 配置文件")
	fs.StringVar(&f.TunnelHost, "tunnel-host", "", "隧道域名")
	fs.Var((*listFlag)(&f.allowExtFlag), "allow-ext", "追加可直出的后缀，逗号分隔或重复")
	fs.Var((*listFlag)(&f.excludeFlag), "exclude", "追加不扫描的目录名")
	fs.Var((*listFlag)(&f.hiddenFlag), "hidden", "追加不展示但仍可访问的 glob")
	fs.StringVar(&f.LogLevel, "log-level", "", "日志级别")
	fs.StringVar(&f.LogFormat, "log-format", "", "日志格式 text|json")
}

// options 把 flag 里的列表合并进 config.Options。
func (f *cmdFlags) options() config.Options {
	o := f.Options
	o.AllowExt = append(append([]string{}, o.AllowExt...), f.allowExtFlag...)
	o.Exclude = append(append([]string{}, o.Exclude...), f.excludeFlag...)
	o.Hidden = append(append([]string{}, o.Hidden...), f.hiddenFlag...)
	return o
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*l = append(*l, p)
		}
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var f cmdFlags
	f.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	eff, err := config.Load(f.options())
	if err != nil {
		return err
	}
	cfg := eff.Config
	log := newLogger(cfg.Server)

	if err := checkListen(cfg); err != nil {
		return err
	}

	_, set, err := buildAuth(cfg, log)
	if err != nil {
		return err
	}

	specs := make([]site.MountSpec, 0, len(cfg.Mounts))
	for _, m := range cfg.Mounts {
		specs = append(specs, site.MountSpec{Name: m.Name, Host: m.Host, Root: m.Root})
	}
	reg := site.NewRegistry(specs, site.Options{
		Refresh:  cfg.Server.Refresh.Duration,
		Exclude:  cfg.Server.Exclude,
		Hidden:   cfg.Server.Hidden,
		MaxBytes: cfg.Server.MaxFileBytes,
		AllowExt: cfg.Server.AllowExt,
	})

	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", cfg.Server.Listen, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	srv, err := httpapi.New(httpapi.Options{
		Registry:  reg,
		Auth:      set,
		PublicURL: cfg.Server.PublicURL,
		BasePath:  cfg.Server.BasePath,
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		Date:      buildinfo.Date,
		Repo:      repoURL,
		Started:   time.Now(),
	}, log)
	if err != nil {
		_ = ln.Close()
		return err
	}

	var tp tunnel.Provider
	if cfg.Tunnel.Mode == "named" {
		tp, err = tunnel.New(tunnel.Options{
			Mode:       cfg.Tunnel.Mode,
			Binary:     cfg.Tunnel.Binary,
			TokenFile:  cfg.Tunnel.TokenFile,
			ConfigFile: cfg.Tunnel.ConfigFile,
			Hostname:   cfg.Tunnel.Hostname,
			Logger:     log,
		})
		if err != nil {
			_ = ln.Close()
			return err
		}
	}

	hs := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 后台预压缩大资源，避免第一个访客等 mermaid 的 gzip。
	web.Warm()

	reg.Start(ctx)
	if tp != nil {
		if err := tp.Start(ctx); err != nil {
			_ = ln.Close()
			return err
		}
	}

	printBanner(os.Stdout, cfg, eff.File, port, set, tp)

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutCtx)
	}()

	err = hs.Serve(ln)
	if tp != nil {
		_ = tp.Close()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// buildAuth 读取密钥与 epoch，并为每个挂载点解析口令。
func buildAuth(cfg config.Config, log *slog.Logger) (*auth.Auth, *auth.Set, error) {
	secret, err := auth.LoadSecret(cfg.Auth.SecretFile)
	if err != nil {
		return nil, nil, err
	}
	epochPath := filepath.Join(cfg.Auth.StateDir, "epoch")
	epoch, err := auth.LoadEpoch(epochPath)
	if err != nil {
		return nil, nil, err
	}
	cookiePath := cfg.Server.BasePath
	if cookiePath == "" {
		cookiePath = "/"
	}
	a, err := auth.New(auth.Options{
		Secret:     secret,
		Epoch:      epoch,
		EpochPath:  epochPath,
		CookieName: cfg.Auth.CookieName,
		CookiePath: cookiePath,
		TTL:        cfg.Auth.CookieTTL.Duration,
		Idle:       cfg.Auth.IdleTTL.Duration,
		ShareTTL:   cfg.Auth.ShareTTL.Duration,
		Secure:     strings.HasPrefix(strings.ToLower(cfg.Server.PublicURL), "https://"),
		RateLimit: auth.RateLimit{
			Attempts: cfg.Auth.RateLimit.Attempts,
			Window:   cfg.Auth.RateLimit.Window.Duration,
			Lockout:  cfg.Auth.RateLimit.Lockout.Duration,
		},
	})
	if err != nil {
		return nil, nil, err
	}

	scopes := make([]*auth.Scope, 0, len(cfg.Mounts))
	for _, m := range cfg.Mounts {
		var hash, file string
		if m.Auth != nil {
			hash, file = m.Auth.PasswordHash, m.Auth.PasswordFile
		}
		cred, err := auth.ResolveCredential(hash, file, cfg.Auth.StateDir, credentialKey(m.Root))
		if err != nil {
			return nil, nil, fmt.Errorf("挂载点 %s: %w", m.Name, err)
		}
		scopes = append(scopes, a.NewScope(m.Host, cred, auth.RateLimit{
			Attempts: cfg.Auth.RateLimit.Attempts,
			Window:   cfg.Auth.RateLimit.Window.Duration,
			Lockout:  cfg.Auth.RateLimit.Lockout.Duration,
		}))
	}
	return a, auth.NewSet(scopes), nil
}

// credentialKey 用根目录哈希做文件名，避免不同项目同名挂载点互相覆盖。
func credentialKey(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:8])
}

// checkListen 拒绝"非回环 + 没有 public_url"的组合：
// 裸 HTTP 暴露会把口令明文发到公网。
func checkListen(cfg config.Config) error {
	host, _, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("server.listen: %w", err)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if cfg.Server.PublicURL == "" {
		return fmt.Errorf("监听 %s 不是回环地址，但没有配置 server.public_url；"+
			"请改回 127.0.0.1 交给隧道，或明确配置对外 HTTPS 地址", cfg.Server.Listen)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func printBanner(w *os.File, cfg config.Config, configFile string, port int, set *auth.Set, tp tunnel.Provider) {
	base := cfg.Server.BasePath
	fmt.Fprintf(w, "\n%s\n", buildinfo.Summary())
	fmt.Fprintf(w, "  本地   http://127.0.0.1:%d%s/\n", port, base)
	if tp != nil {
		if u := tp.URL(); u != "" {
			fmt.Fprintf(w, "  公网   %s%s/\n", u, base)
		}
	}
	if configFile != "" {
		fmt.Fprintf(w, "  配置   %s\n", configFile)
	}
	for _, sc := range set.All() {
		label := sc.Host()
		if label == "" {
			label = "默认站点"
		}
		cred := sc.Credential()
		fmt.Fprintf(w, "  口令   %s：%s\n", label, cred.Describe())
		if cred.Generated {
			fmt.Fprintf(w, "         首次口令：%s\n", cred.Plain)
		}
	}
	if sc := set.Default(); sc != nil {
		if sc.Host() == "" {
			fmt.Fprintln(w, "  分享   mdserve share")
		} else {
			fmt.Fprintf(w, "  分享   mdserve share --host %s\n", sc.Host())
		}
	}
	fmt.Fprintln(w)
}

func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{
			"version":  buildinfo.Version,
			"commit":   buildinfo.Commit,
			"date":     buildinfo.Date,
			"platform": buildinfo.Platform(),
		})
	}
	fmt.Fprintln(os.Stdout, buildinfo.Summary())
	return nil
}

func cmdConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	var f cmdFlags
	f.register(fs)
	fs.BoolVar(&f.json, "json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := f.options()
	opts.SkipRootCheck = true
	eff, err := config.Load(opts)
	if err != nil {
		return err
	}
	red := eff.Config.Redacted()
	if f.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(red)
	}
	out, err := yamlMarshal(red)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, string(out))
	if eff.File != "" {
		fmt.Fprintf(os.Stdout, "\n# 配置文件：%s\n", eff.File)
	} else {
		fmt.Fprintln(os.Stdout, "\n# 未找到配置文件，全部使用默认值")
	}
	fmt.Fprintln(os.Stdout, "# 非默认来源：")
	for _, key := range sortedKeys(eff.Sources) {
		fmt.Fprintf(os.Stdout, "#   %-28s %s\n", key, eff.Sources[key])
	}
	return nil
}

func cmdPasswd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ContinueOnError)
	var f cmdFlags
	f.register(fs)
	f.length = 20
	fs.IntVar(&f.length, "length", 20, "生成口令的长度")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if f.length < 12 || f.length > 64 {
		return errors.New("--length 需在 12 到 64 之间")
	}
	pw := auth.RandomPassword(f.length)
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "口令：%s\n\n把下面这行写进配置里对应挂载点的 auth：\n  password_hash: %q\n", pw, hash)
	return nil
}

func cmdShare(args []string) error {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	var f cmdFlags
	f.register(fs)
	fs.StringVar(&f.host, "host", "", "为哪个域名生成分享码")
	fs.DurationVar(&f.ttl, "ttl", 0, "有效期（默认取 auth.share_ttl）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := f.options()
	opts.SkipRootCheck = true
	eff, err := config.Load(opts)
	if err != nil {
		return err
	}
	if f.ttl > 0 {
		eff.Config.Auth.ShareTTL = config.Duration{Duration: f.ttl}
	}
	_, set, err := buildAuth(eff.Config, nil)
	if err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSpace(f.host))
	var sc *auth.Scope
	if host == "" {
		// 只有一个默认站点时不必指定域名；多站点配置则必须写清楚。
		if def := set.Default(); def != nil {
			sc = def
			host = def.Host()
		} else {
			return errors.New("需要 --host（分享码与域名绑定）")
		}
	} else {
		var ok bool
		sc, ok = set.For(host)
		if !ok {
			return fmt.Errorf("没有匹配 %s 的挂载点", host)
		}
	}

	code, exp := sc.ShareCode()
	fmt.Fprintf(os.Stdout, "分享码（一次性，%s 过期）：\n\n  %s\n\n", time.Until(exp).Round(time.Minute), code)
	fmt.Fprintf(os.Stdout, "打开 %s%s/-/login 粘贴。兑换后立即失效。\n",
		shareBaseURL(eff.Config, host), eff.Config.Server.BasePath)
	return nil
}

// shareBaseURL 优先用 public_url；本机默认站点则拼 http://127.0.0.1:<port>。
func shareBaseURL(cfg config.Config, host string) string {
	if cfg.Server.PublicURL != "" {
		return strings.TrimSuffix(cfg.Server.PublicURL, "/")
	}
	if host == "" || isLoopbackHost(host) {
		_, port, err := net.SplitHostPort(cfg.Server.Listen)
		if err == nil && port != "" {
			return "http://127.0.0.1:" + port
		}
		return "http://127.0.0.1"
	}
	return "https://" + host
}

func cmdRevoke(args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
	var f cmdFlags
	f.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := f.options()
	opts.SkipRootCheck = true
	eff, err := config.Load(opts)
	if err != nil {
		return err
	}
	path := filepath.Join(eff.Config.Auth.StateDir, "epoch")
	if _, err := auth.RotateEpoch(path); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "已轮换 epoch：所有会话与分享码立即失效。运行中的服务会在 2 秒内重新读取。")
	return nil
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	var f cmdFlags
	f.register(fs)
	fs.BoolVar(&f.yes, "yes", false, "确认执行（默认只预览）")
	fs.BoolVar(&f.purge, "purge", false, "同时删除配置文件")
	fs.BoolVar(&f.keepSecrets, "keep-secrets", false, "保留密钥与 epoch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := f.options()
	opts.SkipRootCheck = true
	eff, err := config.Load(opts)
	if err != nil {
		return err
	}
	plan := uninstall.Build(eff.Config, eff.File, uninstall.Options{Purge: f.purge, KeepSecrets: f.keepSecrets})
	plan.Describe(os.Stdout)
	if !f.yes {
		fmt.Fprintln(os.Stdout, "\n这是预览。确认删除请加 --yes。")
		return nil
	}
	fmt.Fprintln(os.Stdout)
	return plan.Apply(func(format string, args ...any) { fmt.Fprintf(os.Stdout, format+"\n", args...) })
}

func newLogger(s config.Server) *slog.Logger {
	level := slog.LevelInfo
	switch s.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	if s.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String("t", t.Format("15:04:05"))
				}
			}
			return a
		},
	}))
}

func yamlMarshal(v any) ([]byte, error) {
	return yaml.Marshal(v)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
