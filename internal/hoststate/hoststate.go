// Package hoststate 记住上一次启动时生效的域名集合。
//
// 它的唯一用途是发现「被改掉的旧域名」：用户把 mounts[].host 从 A 改成 B
// 之后，Cloudflare 上的 DNS 记录和隧道 ingress 不会跟着消失，而 mdserve
// 没有那边的凭据，也不该替用户删。所以这里只做本地记录，让下次启动能提醒
// 一句「A 已经不在配置里了」。
//
// 历史记录永远不该阻止服务启动：文件缺失、损坏、没权限都当作「没有基线」。
package hoststate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshot 是一次启动时生效的域名集合。
type Snapshot struct {
	Hosts   []string  `json:"hosts"`
	Updated time.Time `json:"updated"`
}

// Load 读取快照。读不到或解析失败时返回零值，不返回错误。
func Load(path string) Snapshot {
	b, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}
	}
	return s
}

// Save 原子写入快照：先写同目录临时文件，再 rename，权限 0600。
func Save(path string, hosts []string) error {
	if path == "" {
		return errors.New("hoststate: 路径为空")
	}
	body, err := json.MarshalIndent(Snapshot{
		Hosts:   normalize(hosts),
		Updated: time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hosts-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Removed 返回 prev 里有、current 里没有的域名，已排序。
func Removed(prev, current []string) []string {
	cur := make(map[string]struct{}, len(current))
	for _, h := range normalize(current) {
		cur[h] = struct{}{}
	}
	var out []string
	for _, h := range normalize(prev) {
		if _, ok := cur[h]; !ok {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// normalize 去空白、转小写、去末尾的点、去重并排序。
func normalize(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}
