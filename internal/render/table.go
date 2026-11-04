package render

import (
	gast "github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// tableWrapper 给表格套一层 .table-wrap，让宽表格能横向滚动而不是撑破页面。
// 只接管 ast.KindTable，单元格仍由 GFM 的渲染器处理。
type tableWrapper struct{}

func newTableWrapper() renderer.NodeRenderer { return &tableWrapper{} }

func (t *tableWrapper) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(extast.KindTable, t.renderTable)
}

func (t *tableWrapper) renderTable(w util.BufWriter, source []byte, n gast.Node, entering bool) (gast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString(`<div class="table-wrap"><table`)
		if n.Attributes() != nil {
			html.RenderAttributes(w, n, html.GlobalAttributeFilter)
		}
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</table></div>\n")
	}
	return gast.WalkContinue, nil
}
