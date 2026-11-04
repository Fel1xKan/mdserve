package site

import (
	"html/template"
	"os"
	"path/filepath"

	"github.com/Fel1xKan/mdserve/internal/render"
)

// Doc 是渲染好的一篇文档。按 mtime+size 缓存，文件改动后自动重渲染。
type Doc struct {
	Path       string
	Title      string
	HTML       template.HTML
	Headings   []render.Heading
	HasMermaid bool
	ModTime    int64
	Size       int64
}

type cachedDoc struct {
	doc  *Doc
	mod  int64
	size int64
}

// Doc 读取并渲染 rel 指向的 markdown。缓存命中时不读盘。
func (m *Mount) Doc(rel string) (*Doc, error) {
	abs := filepath.Join(m.Root, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	if err != nil {
		return nil, ErrNotFound
	}
	if info.IsDir() {
		return nil, ErrNotFound
	}
	mod, size := info.ModTime().Unix(), info.Size()

	m.docMu.Lock()
	if c, ok := m.docs[rel]; ok && c.mod == mod && c.size == size {
		m.docMu.Unlock()
		return c.doc, nil
	}
	m.docMu.Unlock()

	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	res, err := m.renderer.Render(src)
	if err != nil {
		return nil, err
	}
	title := res.Title
	if title == "" {
		title = titleFromName(filepath.Base(rel))
	}
	doc := &Doc{
		Path:       rel,
		Title:      title,
		HTML:       res.HTML,
		Headings:   res.Headings,
		HasMermaid: res.HasMermaid,
		ModTime:    mod,
		Size:       size,
	}

	m.docMu.Lock()
	m.docs[rel] = &cachedDoc{doc: doc, mod: mod, size: size}
	m.docMu.Unlock()
	return doc, nil
}

// TOC 返回该文档的侧栏目录 HTML。
func (d *Doc) TOC() template.HTML { return render.TOC(d.Headings) }
