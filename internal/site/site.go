// Package site 把磁盘上的 markdown 目录建模成站点：扫描、路由解析、
// 目录树、文档缓存与全文检索。
package site

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Fel1xKan/mdserve/internal/render"
)

// ErrNotFound 表示路径在站点里不存在。
var ErrNotFound = errors.New("not found")

// Kind 是路径解析结果的类型。
type Kind int

const (
	// KindDoc 命中一篇 markdown。
	KindDoc Kind = iota
	// KindDir 命中一个目录，需要列目录或回退到首页。
	KindDir
	// KindAsset 命中允许直出的静态文件。
	KindAsset
	// KindMissing 什么都没有。
	KindMissing
)

// Options 是站点级参数。
type Options struct {
	Refresh  time.Duration
	Exclude  []string
	MaxBytes int64
	// AllowExt 是允许直出的后缀（小写、带点）。
	AllowExt []string
	// Hidden 是"不展示在树/列表/搜索里，但知道路径仍可访问"的 glob。
	Hidden []string
}

// Node 是目录树节点。
type Node struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Type     string  `json:"type"` // dir | file
	Title    string  `json:"title,omitempty"`
	ModTime  int64   `json:"mtime"`
	Size     int64   `json:"size,omitempty"`
	Children []*Node `json:"children,omitempty"`
	// Hidden 为真时不出现在树、目录列表与搜索结果里，但 Resolve 仍能命中。
	Hidden bool `json:"-"`
}

// Resolved 是一次路径解析的结果。
type Resolved struct {
	Kind Kind
	// Path 是命中的相对路径（doc 或 asset）。
	Path string
	// Dir 是目录路径（KindDir 时有效）。
	Dir string
}

// Mount 是一个挂载点：一个根目录，可能绑定一个域名。
type Mount struct {
	Name string
	Host string
	Root string

	opt      Options
	renderer *render.Renderer

	mu      sync.RWMutex
	tree    []*Node
	files   map[string]*Node  // rel -> 文件节点
	dirs    map[string]*Node  // rel -> 目录节点
	lower   map[string]string // 小写 rel -> 真实 rel（大小写不敏感兜底）
	built   time.Time
	scanErr error

	docMu sync.Mutex
	docs  map[string]*cachedDoc
}

// NewMount 构造挂载点。调用方随后应调用 Scan。
func NewMount(name, host, root string, opt Options) *Mount {
	return &Mount{
		Name:     name,
		Host:     host,
		Root:     root,
		opt:      opt,
		renderer: render.New(),
		files:    map[string]*Node{},
		dirs:     map[string]*Node{},
		lower:    map[string]string{},
		docs:     map[string]*cachedDoc{},
	}
}

// Start 启动后台重建。文件内容变化是请求时按 mtime 校验的，
// 这里只负责让"新增/删除文件"在 Refresh 之后可见。
func (m *Mount) Start(ctx context.Context) {
	if m.opt.Refresh <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(m.opt.Refresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = m.Scan()
			}
		}
	}()
}

// Scan 重建目录树。并发调用是安全的：先算完再整体替换。
func (m *Mount) Scan() error {
	files := map[string]*Node{}
	dirs := map[string]*Node{"": {Name: m.Name, Path: "", Type: "dir"}}
	subdirs := map[string][]string{}
	filesByDir := map[string][]*Node{}

	walkErr := filepath.WalkDir(m.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// 权限问题不该让整站挂掉，跳过这一支。
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(m.Root, p)
		if relErr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()

		if d.IsDir() {
			if m.skipDir(name, rel) {
				return fs.SkipDir
			}
			dirs[rel] = &Node{Name: name, Path: rel, Type: "dir", Hidden: m.isHidden(rel, name)}
			parent := parentDir(rel)
			subdirs[parent] = append(subdirs[parent], rel)
			return nil
		}
		if !isMarkdown(name) || m.skipFile(name) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		n := &Node{
			Name:    name,
			Path:    rel,
			Type:    "file",
			Title:   titleOf(filepath.Join(m.Root, filepath.FromSlash(rel)), name),
			ModTime: info.ModTime().Unix(),
			Size:    info.Size(),
			Hidden:  m.isHidden(rel, name),
		}
		files[rel] = n
		dir := parentDir(rel)
		filesByDir[dir] = append(filesByDir[dir], n)
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	// 自底向上拼装子节点，保证父目录拿到已排序的 children。
	paths := make([]string, 0, len(dirs))
	for p := range dirs {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })

	for _, p := range paths {
		node := dirs[p]
		kids := make([]*Node, 0, len(subdirs[p])+len(filesByDir[p]))
		for _, sub := range subdirs[p] {
			if dirs[sub].Hidden {
				continue
			}
			kids = append(kids, dirs[sub])
		}
		for _, f := range filesByDir[p] {
			if f.Hidden {
				continue
			}
			kids = append(kids, f)
		}
		sortNodes(kids)
		node.Children = kids
	}

	lower := make(map[string]string, len(files)+len(dirs))
	for rel := range dirs {
		if rel != "" {
			lower[strings.ToLower(rel)] = rel
		}
	}
	for rel := range files {
		lower[strings.ToLower(rel)] = rel
	}

	m.mu.Lock()
	m.tree = dirs[""].Children
	m.files = files
	m.dirs = dirs
	m.lower = lower
	m.built = time.Now()
	m.scanErr = nil
	m.mu.Unlock()
	return nil
}

// Tree 返回根目录的子节点，供侧边栏渲染。
func (m *Mount) Tree() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tree
}

// Count 返回 markdown 文件数。
func (m *Mount) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.files)
}

// Built 返回上次扫描完成时间。
func (m *Mount) Built() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.built
}

// Entries 返回某个目录下的直接子节点（目录在前）。
func (m *Mount) Entries(dir string) []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if d, ok := m.dirs[dir]; ok {
		return d.Children
	}
	return nil
}

// Resolve 把 URL 路径解析成文档、目录或静态资源。
// rel 是去掉前导斜杠、已清理过的相对路径。
func (m *Mount) Resolve(rel string) Resolved {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "." {
		rel = ""
	}
	trailingSlash := strings.HasSuffix(rel, "/")

	m.mu.RLock()
	defer m.mu.RUnlock()

	if rel == "" {
		if doc, ok := m.indexDoc(""); ok {
			return Resolved{Kind: KindDoc, Path: doc}
		}
		return Resolved{Kind: KindDir, Dir: ""}
	}

	if trailingSlash {
		dir := strings.TrimSuffix(rel, "/")
		return m.resolveDir(dir)
	}

	if isMarkdown(rel) {
		if real, ok := m.lookupFile(rel); ok {
			return Resolved{Kind: KindDoc, Path: real}
		}
		return Resolved{Kind: KindMissing}
	}

	if m.isAsset(rel) {
		return Resolved{Kind: KindAsset, Path: m.lookupLower(rel)}
	}

	// 无后缀：优先同名 .md，其次目录。
	for _, cand := range []string{rel + ".md", rel + ".markdown"} {
		if real, ok := m.lookupFile(cand); ok {
			return Resolved{Kind: KindDoc, Path: real}
		}
	}
	if _, ok := m.dirs[rel]; ok {
		return m.resolveDir(rel)
	}
	if real := m.lookupLower(rel); real != rel {
		if _, ok := m.dirs[real]; ok {
			return m.resolveDir(real)
		}
	}
	return Resolved{Kind: KindMissing}
}

// resolveDir 在目录里找首页（README/index），找不到就列目录。
// Dir 始终回填，调用方据此把 /guide 重定向到 /guide/，
// 否则页面里的相对链接会解析到错误的层级。
func (m *Mount) resolveDir(dir string) Resolved {
	if doc, ok := m.indexDoc(dir); ok {
		return Resolved{Kind: KindDoc, Path: doc, Dir: dir}
	}
	return Resolved{Kind: KindDir, Dir: dir}
}

// indexDoc 按固定优先级找目录首页。
func (m *Mount) indexDoc(dir string) (string, bool) {
	for _, name := range []string{"README.md", "readme.md", "Readme.md", "index.md", "INDEX.md", "_index.md"} {
		rel := name
		if dir != "" {
			rel = dir + "/" + name
		}
		if real, ok := m.lookupFile(rel); ok {
			return real, true
		}
	}
	return "", false
}

// lookupFile 精确匹配，失败再按小写匹配一次（macOS/Windows 常见大小写混用）。
func (m *Mount) lookupFile(rel string) (string, bool) {
	if _, ok := m.files[rel]; ok {
		return rel, true
	}
	if real, ok := m.lower[strings.ToLower(rel)]; ok {
		if _, isFile := m.files[real]; isFile {
			return real, true
		}
	}
	return "", false
}

func (m *Mount) lookupLower(rel string) string {
	if _, ok := m.files[rel]; ok {
		return rel
	}
	if real, ok := m.lower[strings.ToLower(rel)]; ok {
		return real
	}
	return rel
}

func (m *Mount) isAsset(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	for _, allowed := range m.opt.AllowExt {
		if ext == allowed {
			return true
		}
	}
	return false
}

// AssetPath 把相对路径换成绝对路径，并确认它没有跳出根目录
// （包括通过符号链接跳出去）。
func (m *Mount) AssetPath(rel string) (string, error) {
	clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", ErrNotFound
	}
	abs := filepath.Join(m.Root, filepath.FromSlash(clean))
	realRoot, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", ErrNotFound
	}
	inside, err := filepath.Rel(realRoot, real)
	if err != nil || strings.HasPrefix(inside, "..") {
		return "", ErrNotFound
	}
	info, err := os.Stat(real)
	if err != nil || info.IsDir() {
		return "", ErrNotFound
	}
	return real, nil
}

func (m *Mount) skipDir(name, rel string) bool {
	if strings.HasPrefix(name, ".") && name != "." {
		return true
	}
	for _, ex := range m.opt.Exclude {
		if ex == "" {
			continue
		}
		if name == ex || strings.HasSuffix(rel, "/"+ex) || rel == ex {
			return true
		}
	}
	return false
}

func (m *Mount) skipFile(name string) bool {
	return strings.HasPrefix(name, ".")
}

// isHidden 判断 rel 是否命中 hidden glob。不含 "/" 的 glob 按文件名匹配，
// 这样 "draft.md" 能匹配任意层级；含 "/" 的按相对路径匹配。
func (m *Mount) isHidden(rel, name string) bool {
	for _, g := range m.opt.Hidden {
		if g == "" {
			continue
		}
		if ok, _ := path.Match(g, rel); ok {
			return true
		}
		if !strings.Contains(g, "/") {
			if ok, _ := path.Match(g, name); ok {
				return true
			}
		}
	}
	return false
}

// Registry 按 Host 把请求分派到挂载点。
type Registry struct {
	mounts []*Mount
	byHost map[string]*Mount
	def    *Mount
}

// MountSpec 是构造 Registry 的输入。
type MountSpec struct {
	Name string
	Host string
	Root string
}

// NewRegistry 建立挂载点并做一次扫描。扫描失败只记录在 Mount 上，
// 不影响其他挂载点启动。
func NewRegistry(specs []MountSpec, opt Options) *Registry {
	r := &Registry{byHost: map[string]*Mount{}}
	for _, s := range specs {
		m := NewMount(s.Name, s.Host, s.Root, opt)
		_ = m.Scan()
		r.mounts = append(r.mounts, m)
		if s.Host == "" {
			if r.def == nil {
				r.def = m
			}
			continue
		}
		r.byHost[normalizeHost(s.Host)] = m
	}
	// 注意：不要在这里把"唯一的挂载点"当成默认站点。那会让配置了 host 的
	// 单站点接受任意 Host，Host 白名单（421）就形同虚设。
	return r
}

// Lookup 根据 Host 头选挂载点。host 可以带端口。
func (r *Registry) Lookup(host string) *Mount {
	h := normalizeHost(host)
	if m, ok := r.byHost[h]; ok {
		return m
	}
	// 允许配置里写根域，请求是子域时退到默认站点。
	return r.def
}

// Default 返回默认挂载点，可能为 nil。
func (r *Registry) Default() *Mount { return r.def }

// Mounts 返回全部挂载点。
func (r *Registry) Mounts() []*Mount { return r.mounts }

// Start 为所有挂载点启动后台重建。
func (r *Registry) Start(ctx context.Context) {
	for _, m := range r.mounts {
		m.Start(ctx)
	}
}

// RefreshAll 立即重建全部挂载点。
func (r *Registry) RefreshAll() {
	for _, m := range r.mounts {
		_ = m.Scan()
	}
}

// normalizeHost 去掉端口、转小写、去掉末尾的点。
func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.HasSuffix(host, "]") {
		// 端口只可能是最后一段数字，避免误伤 IPv6。
		if isAllDigits(host[i+1:]) {
			host = host[:i]
		}
	}
	host = strings.Trim(host, "[]")
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parentDir 返回父目录，根目录统一用空串表示。
func parentDir(rel string) string {
	d := path.Dir(rel)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

// sortNodes 目录在前、文件在后，同类按自然序（chapter2 在 chapter10 前）。
func sortNodes(nodes []*Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Type != b.Type {
			return a.Type == "dir"
		}
		return naturalLess(a.Name, b.Name)
	})
}

func naturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0
	for i < len(la) && j < len(lb) {
		ca, cb := la[i], lb[j]
		if isDigit(ca) && isDigit(cb) {
			si, sj := i, j
			for i < len(la) && isDigit(la[i]) {
				i++
			}
			for j < len(lb) && isDigit(lb[j]) {
				j++
			}
			na := strings.TrimLeft(la[si:i], "0")
			nb := strings.TrimLeft(lb[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(la)-i < len(lb)-j
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// titleOf 读文件头部找第一个一级标题，找不到就用文件名。
func titleOf(abs, name string) string {
	f, err := os.Open(abs)
	if err != nil {
		return titleFromName(name)
	}
	defer f.Close()

	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if strings.HasPrefix(line, "# ") {
			if t := strings.TrimSpace(strings.TrimPrefix(line, "# ")); t != "" {
				return t
			}
		}
	}
	return titleFromName(name)
}

func titleFromName(name string) string {
	base := strings.TrimSuffix(name, path.Ext(name))
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	if base == "" {
		return name
	}
	return base
}
