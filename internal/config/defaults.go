package config

import (
	"fmt"
	"time"
)

// sprint 是给错误信息用的小工具，避免为了一个 %v 引入 fmt 到多处。
func sprint(v any) string { return fmt.Sprintf("%v", v) }

// Default 返回内置默认值。任何"用户没配"的项都从这里来，
// 所以默认值必须是安全的：只监听回环、开启鉴权、不自动下载任何东西。
func Default() Config {
	return Config{
		Version: CurrentVersion,
		Server: Server{
			Listen:       "127.0.0.1:8787",
			PublicURL:    "",
			BasePath:     "",
			Refresh:      Duration{10 * time.Second},
			MaxFileBytes: 4 << 20,
			AllowExt:     defaultAllowExt(),
			Exclude:      defaultExclude(),
			Hidden:       nil,
			Language:     "zh",
			LogLevel:     "info",
			LogFormat:    "text",
		},
		Mounts: nil,
		Auth: Auth{
			SecretFile: "~/.config/mdserve/secret",
			StateDir:   "~/.local/state/mdserve",
			CookieName: "mdserve_s",
			CookieTTL:  Duration{8 * time.Hour},
			IdleTTL:    Duration{30 * time.Minute},
			ShareTTL:   Duration{1 * time.Hour},
			RateLimit: RateLimit{
				Attempts: 8,
				Window:   Duration{10 * time.Minute},
				Lockout:  Duration{10 * time.Minute},
			},
		},
		Tunnel: Tunnel{
			Provider: "cloudflared",
			Mode:     "none",
			Binary:   "cloudflared",
		},
	}
}

// defaultAllowExt 是直出白名单。只放"浏览器不会执行"的类型，
// 加上 pdf/office 这类文档；绝不放 html/js，避免同源 XSS。
// svg 在列表里，但处理器会强制 attachment + CSP sandbox。
func defaultAllowExt() []string {
	return []string{
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".avif", ".ico",
		".pdf", ".zip", ".csv", ".txt", ".json", ".yaml", ".yml", ".toml",
		".mp4", ".webm", ".mp3", ".wav",
		".xlsx", ".docx", ".pptx",
		".woff2", ".woff",
	}
}

// defaultExclude 是根本不进索引的路径片段。
func defaultExclude() []string {
	return []string{".git", "node_modules", ".obsidian", ".idea", ".vscode"}
}

// Normalize 把用户输入的后缀统一成小写、带点、去重的形态。
// 配置里写 "PNG"、"png"、".png" 都得到 ".png"。
func Normalize(exts []string) []string {
	seen := make(map[string]struct{}, len(exts))
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		if e == "" {
			continue
		}
		if e[0] != '.' {
			e = "." + e
		}
		e = lowerASCII(e)
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}

// NormalizeBasePath 把 "/notes/"、"/notes"、"notes" 统一成 "/notes"，
// 空值或 "/" 统一成 ""（表示根路径）。返回值非法时报错。
func NormalizeBasePath(p string) (string, error) {
	p = trimSpace(p)
	if p == "" || p == "/" {
		return "", nil
	}
	if p[0] != '/' {
		p = "/" + p
	}
	for len(p) > 1 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	if p == "/-" || len(p) > 2 && p[:2] == "/-" && p[2] == '/' {
		return "", fmt.Errorf("base_path %q 与保留前缀 /- 冲突", p)
	}
	if containsAny(p, "..", "?", "#", "\\", "//") {
		return "", fmt.Errorf("base_path %q 含非法片段", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return "", fmt.Errorf("base_path %q 含空白或控制字符", p)
		}
	}
	return p, nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) == 0 {
			continue
		}
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

// lowerASCII 只处理 ASCII，后缀不需要 Unicode 大小写折叠。
func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
