// Package uninstall 清理 mdserve 在本机留下的状态文件。
//
// v1 不再调用 Cloudflare API，也不下载 cloudflared，所以"卸载"只剩本地文件：
// 自动生成的口令、HMAC 密钥、epoch，以及（可选）配置文件本身。
package uninstall

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Fel1xKan/mdserve/internal/config"
)

// Options 控制清理范围。
type Options struct {
	// Purge 为真时连配置文件一起删。
	Purge bool
	// KeepSecrets 为真时保留密钥与 epoch（只删自动生成的口令）。
	KeepSecrets bool
}

// File 是一个待删除的文件。
type File struct {
	Path string
	Kind string
	Size int64
}

// Plan 是清理计划。默认只打印，不执行。
type Plan struct {
	StateDir   string
	SecretFile string
	ConfigFile string
	Files      []File
	Notes      []string
}

// Empty 表示没有可清理的东西。
func (p *Plan) Empty() bool { return p == nil || len(p.Files) == 0 }

// Build 收集要删的文件。它只读目录，不改任何东西。
func Build(cfg config.Config, configFile string, opt Options) *Plan {
	p := &Plan{
		StateDir:   cfg.Auth.StateDir,
		SecretFile: cfg.Auth.SecretFile,
		ConfigFile: configFile,
	}

	if !opt.KeepSecrets {
		p.add(cfg.Auth.SecretFile, "HMAC 密钥")
		p.add(filepath.Join(cfg.Auth.StateDir, "epoch"), "会话 epoch")
		p.Notes = append(p.Notes, "删除密钥与 epoch 会让所有已签发的会话和分享码立即失效。")
	}

	pattern := filepath.Join(cfg.Auth.StateDir, "passwords", "*.pass")
	if matches, err := filepath.Glob(pattern); err == nil {
		sort.Strings(matches)
		for _, m := range matches {
			p.add(m, "自动生成的口令")
		}
	}

	if opt.Purge && configFile != "" {
		p.add(configFile, "配置文件")
		p.Notes = append(p.Notes, "配置文件已删除；如需恢复请从版本库或备份取回。")
	}
	return p
}

func (p *Plan) add(path, kind string) {
	if path == "" {
		return
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return
	}
	p.Files = append(p.Files, File{Path: path, Kind: kind, Size: st.Size()})
}

// Describe 打印将要删除的内容。
func (p *Plan) Describe(w io.Writer) {
	if p.Empty() {
		fmt.Fprintln(w, "没有需要清理的本地状态。")
		return
	}
	fmt.Fprintln(w, "将删除以下文件：")
	for _, f := range p.Files {
		fmt.Fprintf(w, "  - %s  (%s, %d 字节)\n", f.Path, f.Kind, f.Size)
	}
	for _, n := range p.Notes {
		fmt.Fprintf(w, "注意：%s\n", n)
	}
}

// Apply 执行删除，并顺手回收空目录。单个文件删不掉不会中断整体。
func (p *Plan) Apply(logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var firstErr error
	for _, f := range p.Files {
		if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			logf("删除 %s 失败: %v", f.Path, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		logf("已删除 %s", f.Path)
	}
	p.pruneEmpty(filepath.Join(p.StateDir, "passwords"))
	p.pruneEmpty(p.StateDir)
	if p.ConfigFile != "" {
		p.pruneEmpty(filepath.Dir(p.ConfigFile))
	}
	return firstErr
}

// pruneEmpty 只在目录为空时删除，避免误删别人的文件。
func (p *Plan) pruneEmpty(dir string) {
	if dir == "" || dir == "/" || dir == "." {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	_ = os.Remove(dir)
}
