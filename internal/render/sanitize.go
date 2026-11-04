package render

import "github.com/microcosm-cc/bluemonday"

// newPolicy 定义允许保留的 HTML。原则：
//   - 允许文档排版需要的标签（表格、图片、折叠块、任务列表）；
//   - 允许 class/id，因为代码高亮与 mermaid 依赖 class；
//   - 不允许 script / iframe / style / 事件属性，避免同源 XSS。
func newPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements(
		"p", "br", "hr", "div", "span", "section", "article", "header", "footer", "aside",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "dl", "dt", "dd",
		"blockquote", "pre", "code", "kbd", "samp", "var", "tt",
		"em", "strong", "b", "i", "u", "s", "del", "ins", "mark", "small", "sub", "sup",
		"abbr", "cite", "q", "time", "ruby", "rt", "rp",
		"figure", "figcaption", "picture", "img", "a",
		"details", "summary",
		"table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption", "colgroup", "col",
		"input", "video", "audio", "source", "track",
	)

	// class/id 全局放行：代码块的 language-*、mermaid 的标记都靠它。
	p.AllowAttrs("class", "id", "title", "lang", "dir").Globally()

	p.AllowAttrs("href", "target").OnElements("a")
	p.AllowAttrs("src", "alt", "width", "height", "loading", "srcset", "sizes").OnElements("img")
	p.AllowAttrs("colspan", "rowspan", "align", "width", "height").OnElements("td", "th")
	p.AllowAttrs("align", "width").OnElements("table", "col", "colgroup")
	p.AllowAttrs("start", "type", "reversed").OnElements("ol")
	p.AllowAttrs("type", "checked", "disabled").OnElements("input")
	p.AllowAttrs("open").OnElements("details")
	p.AllowAttrs("controls", "preload", "poster", "width", "height", "muted", "loop").OnElements("video", "audio")
	p.AllowAttrs("src", "type", "media").OnElements("source")
	p.AllowAttrs("datetime").OnElements("time")

	p.AllowURLSchemes("http", "https", "mailto")
	p.AllowRelativeURLs(true)
	p.AllowDataURIImages()
	p.RequireParseableURLs(true)

	return p
}
