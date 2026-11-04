package httpapi

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"path"
	"strings"

	"github.com/Fel1xKan/mdserve/internal/site"
)

// view 是所有页面模板共用的一份数据。每个模板只取自己需要的字段，
// 这样新增一个门面页时不必再定义一套结构体。
type view struct {
	// Base 是 base_path，模板里所有站内链接都要加这个前缀。
	Base string
	// Asset 是静态资源的整体版本号，模板用它拼 ?v= 让缓存可长期化。
	Asset string

	Title     string
	Product   string
	Version   string
	Commit    string
	Date      string
	Uptime    string
	Repo      string
	RepoLabel string

	// 登录页
	Next  string
	Error string

	// 检索页
	Query   string
	Count   int
	Results []site.SearchResult

	// 文档页
	SiteLabel  string
	BrandSub   string
	TreeHTML   template.HTML
	TOCHTML    template.HTML
	Body       template.HTML
	HasMermaid bool
	Path       string
}

// indexNames 是目录首页的候选文件名（小写比较）。
var indexNames = map[string]bool{
	"readme.md": true,
	"index.md":  true,
	"_index.md": true,
}

// treeHTML 渲染侧边栏目录树。
// 当前文档路径上的目录默认展开，其余折叠。点目录行只负责展开/折叠，
// 目录首页作为第一个子项出现（「概述」），避免「点名字就跳走」和
// 「想展开却跳页」这两个动作抢同一次点击。
func treeHTML(nodes []*site.Node, current, base string) template.HTML {
	var b strings.Builder
	b.WriteString(`<ul>`)
	writeTree(&b, nodes, current, base)
	b.WriteString(`</ul>`)
	// 内容全部经过 html.EscapeString / url.PathEscape，可以安全标记为 HTML。
	return template.HTML(b.String())
}

func writeTree(b *strings.Builder, nodes []*site.Node, current, base string) {
	for _, n := range nodes {
		if n.Type == "dir" {
			idx := indexChild(n)
			// 目录页本身（/erp/）也要展开自己，所以 current 等于 n.Path 也算命中。
			expanded := current == n.Path || strings.HasPrefix(current, n.Path+"/")
			cls := "node-dir"
			aria := "true"
			if !expanded {
				cls += " collapsed"
				aria = "false"
			}
			fmt.Fprintf(b, `<li class="%s"><div class="node-label" role="button" tabindex="0" aria-expanded="%s">`+
				`<span class="caret">▼</span><span class="node-name">%s</span></div>`,
				cls, aria, html.EscapeString(n.Name))

			if len(n.Children) > 0 {
				b.WriteString(`<ul>`)
				if idx != nil {
					icl := "node-file"
					if idx.Path == current || n.Path == current {
						icl += " active"
					}
					fmt.Fprintf(b, `<li class="%s"><a href="%s/%s/" title="%s">概述</a></li>`,
						icl, base, escapePath(n.Path), html.EscapeString(idx.Path))
				}
				for _, c := range n.Children {
					if c == idx {
						continue
					}
					writeTree(b, []*site.Node{c}, current, base)
				}
				b.WriteString(`</ul>`)
			}
			b.WriteString(`</li>`)
			continue
		}

		cls := "node-file"
		if n.Path == current {
			cls += " active"
		}
		label := n.Title
		if label == "" {
			label = n.Name
		}
		fmt.Fprintf(b, `<li class="%s"><a href="%s/%s" title="%s">%s</a></li>`,
			cls, base, escapePath(n.Path), html.EscapeString(n.Path), html.EscapeString(label))
	}
}

// indexChild 找出目录的首页文件（README.md / index.md）。
func indexChild(n *site.Node) *site.Node {
	for _, c := range n.Children {
		if c.Type == "file" && indexNames[strings.ToLower(c.Name)] {
			return c
		}
	}
	return nil
}

// listingHTML 渲染目录列表页。用 HTML 拼装而不是单独模板，
// 是因为它需要按条目类型切换图标，模板里写反而更绕。
func listingHTML(dir string, entries []*site.Node, base string) template.HTML {
	var b strings.Builder
	if dir != "" {
		parent := path.Dir(dir)
		if parent == "." {
			parent = ""
		}
		fmt.Fprintf(&b, `<p><a href="%s/%s">↑ 上一级</a></p>`, base, escapePath(parent))
	}
	title := "文档目录"
	if dir != "" {
		title = path.Base(dir)
	}
	fmt.Fprintf(&b, `<h1>%s</h1>`, html.EscapeString(title))

	if len(entries) == 0 {
		b.WriteString(`<p>这个目录里还没有 markdown 文件。</p>`)
		return template.HTML(b.String())
	}

	b.WriteString(`<ul class="listing">`)
	for _, n := range entries {
		if n.Type == "dir" {
			fmt.Fprintf(&b, `<li><a href="%s/%s/"><span class="ico">📁</span><span class="name">%s/</span></a></li>`,
				base, escapePath(n.Path), html.EscapeString(n.Name))
			continue
		}
		label := n.Title
		if label == "" {
			label = n.Name
		}
		fmt.Fprintf(&b, `<li><a href="%s/%s"><span class="ico">📄</span><span class="name">%s</span><span class="meta">%s</span></a></li>`,
			base, escapePath(n.Path), html.EscapeString(label), humanSize(n.Size))
	}
	b.WriteString(`</ul>`)
	return template.HTML(b.String())
}

// escapePath 按段转义路径，保留斜杠。
func escapePath(rel string) string {
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
