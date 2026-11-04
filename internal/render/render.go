// Package render 把 markdown 渲染成 HTML：goldmark 解析、标题锚点、
// 目录（TOC）、表格横向滚动容器，最后过一遍 HTML 消毒。
package render

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Heading 是渲染过程中收集到的标题，用于生成目录和锚点。
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

// Result 是一次渲染的产物。
type Result struct {
	HTML       template.HTML
	Title      string
	Headings   []Heading
	HasMermaid bool
}

// Renderer 可并发使用：goldmark 的解析器与渲染器都是无状态的，
// 每次渲染的状态（标题去重、收集）都放在局部变量里。
type Renderer struct {
	md     goldmark.Markdown
	policy *bluemonday.Policy
}

// New 构造渲染器。原始 HTML 保留，随后统一经 bluemonday 消毒，消毒不可关闭。
func New() *Renderer {
	opts := []goldmark.Option{
		goldmark.WithExtensions(
			extension.GFM, // 表格、删除线、任务列表、自动链接
			extension.Footnote,
			extension.DefinitionList,
		),
		goldmark.WithParserOptions(
			parser.WithBlockParsers(),
		),
		goldmark.WithRendererOptions(
			// 保留 README 里常见的 <div align="center">、徽章，再统一消毒。
			html.WithUnsafe(),
			// goldmark 按优先级从高到低注册，后注册者生效，
			// 所以数字要比 GFM 表格渲染器的 500 更小才能接管。
			renderer.WithNodeRenderers(util.Prioritized(newTableWrapper(), 100)),
		),
	}
	return &Renderer{md: goldmark.New(opts...), policy: newPolicy()}
}

// Render 解析并渲染一份 markdown。
func (r *Renderer) Render(src []byte) (*Result, error) {
	reader := text.NewReader(src)
	doc := r.md.Parser().Parse(reader)

	res := &Result{}
	seen := make(map[string]int)
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		label := strings.TrimSpace(nodeText(n, src))
		if label == "" {
			return ast.WalkContinue, nil
		}
		id := slug(label)
		if id == "" {
			id = "section"
		}
		if k, dup := seen[id]; dup {
			seen[id] = k + 1
			id = id + "-" + itoa(k+1)
		} else {
			seen[id] = 1
		}
		h.SetAttributeString("id", id)
		res.Headings = append(res.Headings, Heading{Level: h.Level, Text: label, ID: id})
		if res.Title == "" && h.Level == 1 {
			res.Title = label
		}
		return ast.WalkContinue, nil
	})
	if res.Title == "" && len(res.Headings) > 0 {
		res.Title = res.Headings[0].Text
	}

	var buf bytes.Buffer
	if err := r.md.Renderer().Render(&buf, src, doc); err != nil {
		return nil, err
	}
	out := r.policy.Sanitize(buf.String())
	res.HTML = template.HTML(out)
	res.HasMermaid = strings.Contains(out, `class="language-mermaid"`)
	return res, nil
}

// TOC 生成侧栏目录。CSS 里定义的是 li.lv2 / li.lv3 的扁平列表，
// 所以这里不做嵌套，只按层级加 class。
func TOC(headings []Heading) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="toc-title">本页目录</div><ol>`)
	n := 0
	for _, h := range headings {
		if h.Level < 2 || h.Level > 3 {
			continue
		}
		cls := "lv2"
		if h.Level == 3 {
			cls = "lv3"
		}
		b.WriteString(`<li class="` + cls + `"><a href="#`)
		b.WriteString(template.HTMLEscapeString(h.ID))
		b.WriteString(`">`)
		b.WriteString(template.HTMLEscapeString(h.Text))
		b.WriteString(`</a></li>`)
		n++
	}
	if n == 0 {
		return ""
	}
	b.WriteString(`</ol>`)
	return template.HTML(b.String())
}

// slug 生成锚点：保留字母、数字、CJK 与连字符，其余折叠成 '-'。
// 中文标题因此也能得到可读的锚点，而不是一串空白。
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		case isCJK(r):
			b.WriteRune(r)
			dash = false
		case r == '-' || r == ' ' || r == '.' || r == '/' || r == ':':
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // 基本汉字
		(r >= 0x3400 && r <= 0x4DBF) || // 扩展 A
		(r >= 0x3040 && r <= 0x30FF) || // 日文假名
		(r >= 0xAC00 && r <= 0xD7AF) // 韩文
}

// nodeText 提取节点的纯文本，用于标题与锚点。
func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
