// Package config 负责 mdserve 的配置：类型、默认值、加载与来源标注。
//
// 优先级：CLI 参数 > 环境变量 > YAML 文件 > 内置默认值。
// 合并规则：标量覆盖，列表（allow_ext / exclude / hidden）追加，mounts 整体替换。
//
// 口令只接受 password_file / password_hash，不接受明文或环境变量。
package config

import "time"

// CurrentVersion 是配置文件格式版本。目前只有 1，读到更高版本直接报错，
// 而不是猜字段含义。
const CurrentVersion = 1

// Config 是整个服务端配置。字段的 yaml tag 就是文档里写的那套键名。
type Config struct {
	Version int     `yaml:"version" json:"version"`
	Server  Server  `yaml:"server" json:"server"`
	Mounts  []Mount `yaml:"mounts" json:"mounts"`
	Auth    Auth    `yaml:"auth" json:"auth"`
	Tunnel  Tunnel  `yaml:"tunnel" json:"tunnel"`
}

// Server 是进程级设置，对所有挂载点生效。
type Server struct {
	// Listen 是本地监听地址。默认只绑 127.0.0.1：对外暴露交给隧道。
	// 非回环监听必须同时配置 public_url，否则启动被拒绝。
	Listen string `yaml:"listen" json:"listen"`
	// PublicURL 是外部访问地址，用于生成分享链接和 cookie 的 Secure 判断。
	// 留空时按请求推断（隧道模式下由 cloudflared 的 URL 填充）。
	PublicURL string `yaml:"public_url" json:"public_url"`
	// BasePath 让站点挂在域名的子路径下，例如 "/notes"。
	// 空字符串表示挂在根路径。
	BasePath string `yaml:"base_path" json:"base_path"`
	// Refresh 是目录树重建间隔。文件内容每次请求都会 stat 校验 mtime，
	// 这个值只影响"新增/删除文件"的可见延迟。
	Refresh Duration `yaml:"refresh" json:"refresh"`
	// MaxFileBytes 是单个文件的最大读取字节数，超过直接 413。
	MaxFileBytes int64 `yaml:"max_file_bytes" json:"max_file_bytes"`
	// AllowExt 是允许直出的静态资源后缀（含点，小写）。不含 html/js。
	AllowExt []string `yaml:"allow_ext" json:"allow_ext"`
	// Exclude 是扫描时完全忽略的路径片段（目录名或文件后缀）。
	Exclude []string `yaml:"exclude" json:"exclude"`
	// Hidden 是"不显示在目录树与列表里、但知道路径仍可访问"的 glob。
	// 与 Exclude 的区别：Exclude 根本不进索引，Hidden 只是不展示。
	Hidden []string `yaml:"hidden" json:"hidden"`
	// Language 影响 UI 文案，目前支持 zh 与 en。
	Language string `yaml:"language" json:"language"`
	// LogLevel 为 debug / info / warn / error。
	LogLevel string `yaml:"log_level" json:"log_level"`
	// LogFormat 为 text 或 json。json 便于审计日志落盘后检索。
	LogFormat string `yaml:"log_format" json:"log_format"`
}

// Mount 是一个"目录 -> 站点"的映射。多个挂载点按 Host 区分。
type Mount struct {
	Name string `yaml:"name" json:"name"`
	// Host 是用于路由的域名（不含端口）。留空表示默认站点，
	// 未匹配到任何 Host 的请求都落到它上面；最多只能有一个。
	Host string `yaml:"host" json:"host"`
	Root string `yaml:"root" json:"root"`
	// Auth 可选，用于覆盖全局口令来源。
	Auth *MountAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// MountAuth 是挂载点级的口令来源。两者任填其一，优先 password_hash。
// 只接受文件或哈希，不接受明文口令。
type MountAuth struct {
	PasswordFile string `yaml:"password_file" json:"-"`
	PasswordHash string `yaml:"password_hash" json:"-"`
}

// Auth 是访问控制配置。
type Auth struct {
	// SecretFile 是 HMAC 密钥文件（32 字节 hex，0600）。缺失时自动生成。
	SecretFile string `yaml:"secret_file" json:"secret_file"`
	// StateDir 存放 epoch 与自动生成的口令文件，默认 ~/.local/state/mdserve。
	StateDir string `yaml:"state_dir" json:"state_dir"`
	// CookieName 是会话 cookie 名。cookie 由浏览器按 host 隔离，
	// 所以多挂载点之间天然不共享。
	CookieName string `yaml:"cookie_name" json:"cookie_name"`
	// CookieTTL 是会话绝对有效期。
	CookieTTL Duration `yaml:"cookie_ttl" json:"cookie_ttl"`
	// IdleTTL 是空闲有效期，每次成功请求都会续期。
	IdleTTL Duration `yaml:"idle_ttl" json:"idle_ttl"`
	// ShareTTL 是一次性分享码的有效期。
	ShareTTL  Duration  `yaml:"share_ttl" json:"share_ttl"`
	RateLimit RateLimit `yaml:"rate_limit" json:"rate_limit"`
}

// RateLimit 限制暴力猜口令。
type RateLimit struct {
	Attempts int      `yaml:"attempts" json:"attempts"`
	Window   Duration `yaml:"window" json:"window"`
	Lockout  Duration `yaml:"lockout" json:"lockout"`
}

// Tunnel 是公网暴露配置。
type Tunnel struct {
	// Provider 目前只有 cloudflared。
	Provider string `yaml:"provider" json:"provider"`
	// Mode 为 none / named。
	Mode string `yaml:"mode" json:"mode"`
	// Binary 是 cloudflared 可执行文件路径或名字。只查 PATH 与绝对路径，
	// 不会在运行时下载。
	Binary string `yaml:"binary" json:"binary"`
	// TokenFile 是 named 隧道的 token 文件（0600）。
	TokenFile string `yaml:"token_file" json:"token_file"`
	// ConfigFile 是 cloudflared 自己的 config.yml，与 token 二选一。
	ConfigFile string `yaml:"config" json:"config"`
	// Hostname 是 named 模式下对外域名，用于打印访问地址。
	Hostname string `yaml:"hostname" json:"hostname"`
}

// Duration 让 YAML 里可以写 "10s" / "12h"，而不是裸数字秒。
type Duration struct {
	time.Duration
}

// UnmarshalYAML 支持 "10s"、"500ms" 以及纯数字（按秒）。
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	return d.set(raw)
}

func (d *Duration) set(raw any) error {
	switch v := raw.(type) {
	case string:
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		d.Duration = parsed
	case int:
		d.Duration = time.Duration(v) * time.Second
	case float64:
		d.Duration = time.Duration(v * float64(time.Second))
	case nil:
		d.Duration = 0
	default:
		return &InvalidDurationError{Value: raw}
	}
	return nil
}

// MarshalYAML 让 `mdserve config` 输出可读的 "12h0m0s"。
func (d Duration) MarshalYAML() (any, error) {
	if d.Duration == 0 {
		return "0s", nil
	}
	return d.Duration.String(), nil
}

// MarshalJSON 同上，保持字符串形态。
func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration == 0 {
		return []byte(`"0s"`), nil
	}
	return []byte(`"` + d.Duration.String() + `"`), nil
}

// InvalidDurationError 在 YAML 写了无法解析的时长时给出可定位的错误。
type InvalidDurationError struct{ Value any }

func (e *InvalidDurationError) Error() string {
	return "invalid duration: expected \"10s\" or seconds, got " + sprint(e.Value)
}
