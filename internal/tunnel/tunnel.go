// Package tunnel 管理 cloudflared 子进程。只支持 named 隧道；
// cloudflared 必须预装，不会在运行时下载。
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Provider 是一条隧道。URL 在 named 模式下由配置给出，
// 不依赖解析 cloudflared 的输出。
type Provider interface {
	Start(ctx context.Context) error
	URL() string
	Close() error
}

// Options 是 New 的入参。
type Options struct {
	Mode       string
	Binary     string
	TokenFile  string
	ConfigFile string
	Hostname   string
	Logger     *slog.Logger
}

// New 按模式返回 Provider。mode=none 返回 (nil, nil)，调用方需判空。
func New(opt Options) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(opt.Mode)) {
	case "", "none":
		return nil, nil
	case "named":
		if opt.TokenFile == "" && opt.ConfigFile == "" {
			return nil, errors.New("tunnel: named 模式需要 token_file 或 config")
		}
		if opt.Hostname == "" {
			return nil, errors.New("tunnel: named 模式需要 hostname")
		}
		log := opt.Logger
		if log == nil {
			log = slog.Default()
		}
		return &cloudflared{opt: opt, log: log, done: make(chan struct{})}, nil
	default:
		return nil, fmt.Errorf("tunnel: 不支持的模式 %q（只支持 none/named）", opt.Mode)
	}
}

type cloudflared struct {
	opt Options
	log *slog.Logger

	mu     sync.Mutex
	cmd    *exec.Cmd
	closed bool
	done   chan struct{}
}

// URL 是 named 隧道的公开地址。
func (c *cloudflared) URL() string {
	if c.opt.Hostname == "" {
		return ""
	}
	return "https://" + c.opt.Hostname
}

// Start 校验二进制与 token 后启动监督循环。它立即返回，
// 进程退出/重启在后台处理。
func (c *cloudflared) Start(ctx context.Context) error {
	bin, err := ensureBinary(c.opt.Binary)
	if err != nil {
		return err
	}
	token, err := readToken(c.opt.TokenFile)
	if err != nil {
		return err
	}
	go c.supervise(ctx, bin, token)
	return nil
}

// Close 停止子进程并等待监督循环退出。
func (c *cloudflared) Close() error {
	c.mu.Lock()
	c.closed = true
	cmd := c.cmd
	c.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	return nil
}

func (c *cloudflared) supervise(ctx context.Context, bin, token string) {
	defer close(c.done)
	backoff := time.Second
	for {
		started := time.Now()
		err := c.runOnce(ctx, bin, token)
		if ctx.Err() != nil || c.isClosed() {
			return
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}
		c.log.Warn("cloudflared 退出，准备重启", "err", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (c *cloudflared) runOnce(ctx context.Context, bin, token string) error {
	args := []string{"tunnel", "--no-autoupdate", "run"}
	if c.opt.ConfigFile != "" {
		args = append(args, "--config", c.opt.ConfigFile)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// token 走环境变量，不出现在 argv 里（ps 可见）。
	cmd.Env = os.Environ()
	if token != "" {
		cmd.Env = append(cmd.Env, "TUNNEL_TOKEN="+token)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = cmd.Process.Kill()
		return errors.New("已关闭")
	}
	c.cmd = cmd
	c.mu.Unlock()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			c.log.Debug("cloudflared", "line", line)
		}
	}
	return cmd.Wait()
}

func (c *cloudflared) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// ensureBinary 只查显式路径与 PATH，绝不下载。
func ensureBinary(name string) (string, error) {
	if name == "" {
		name = "cloudflared"
	}
	if strings.ContainsRune(name, os.PathSeparator) {
		st, err := os.Stat(name)
		if err != nil {
			return "", fmt.Errorf("cloudflared: %w", err)
		}
		if st.IsDir() {
			return "", fmt.Errorf("cloudflared: %s 是目录", name)
		}
		return name, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("找不到 cloudflared：请先安装（apt install cloudflared 或 brew install cloudflared），或用 tunnel.binary 指定绝对路径")
	}
	return p, nil
}

func readToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("隧道 token 文件 %s: %w", path, err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("隧道 token 文件 %s 是目录", path)
	}
	if perm := st.Mode().Perm(); perm&0o007 != 0 {
		return "", fmt.Errorf("隧道 token 文件 %s 权限为 %04o，其他用户可访问；请 chmod 600", path, perm)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("隧道 token 文件 %s 为空", path)
	}
	return token, nil
}
