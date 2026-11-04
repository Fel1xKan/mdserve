package render

import (
	"strings"
	"testing"
)

func render(t *testing.T, src string) *Result {
	t.Helper()
	res, err := New().Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return res
}

func TestHeadingIDsAndTitle(t *testing.T) {
	res := render(t, "# 项目文档\n\n## 快速开始\n\n### Sub Section\n\n## 快速开始\n")
	if res.Title != "项目文档" {
		t.Fatalf("Title = %q", res.Title)
	}
	html := string(res.HTML)
	for _, want := range []string{`id="项目文档"`, `id="快速开始"`, `id="sub-section"`, `id="快速开始-2"`} {
		if !strings.Contains(html, want) {
			t.Errorf("缺少锚点 %s\n%s", want, html)
		}
	}
	if len(res.Headings) != 4 {
		t.Fatalf("Headings = %d, want 4", len(res.Headings))
	}
}

func TestTableWrapper(t *testing.T) {
	res := render(t, "| a | b |\n|---|---|\n| 1 | 2 |\n")
	if !strings.Contains(string(res.HTML), `<div class="table-wrap"><table>`) {
		t.Fatalf("表格没有被包裹：\n%s", res.HTML)
	}
	if !strings.Contains(string(res.HTML), "</table></div>") {
		t.Fatalf("表格容器没有闭合：\n%s", res.HTML)
	}
}

func TestSanitizeRemovesScript(t *testing.T) {
	res := render(t, "# t\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n")
	html := string(res.HTML)
	if strings.Contains(html, "<script") {
		t.Fatalf("script 未被移除：\n%s", html)
	}
	if strings.Contains(html, "onerror") {
		t.Fatalf("事件属性未被移除：\n%s", html)
	}
}

func TestMermaidDetection(t *testing.T) {
	withChart := render(t, "```mermaid\ngraph TD; A-->B;\n```\n")
	if !withChart.HasMermaid {
		t.Fatal("应检测到 mermaid")
	}
	if withChart.HasMermaid = render(t, "```go\nfmt.Println()\n```\n").HasMermaid; withChart.HasMermaid {
		t.Fatal("普通代码块不应被当成 mermaid")
	}
}

func TestTOCOnlyLevel23(t *testing.T) {
	res := render(t, "# 标题\n\n## 一级\n\n### 二级\n\n#### 太深\n")
	toc := string(TOC(res.Headings))
	if !strings.Contains(toc, `class="lv2"`) || !strings.Contains(toc, `class="lv3"`) {
		t.Fatalf("TOC 缺少层级：%s", toc)
	}
	if strings.Contains(toc, "太深") {
		t.Fatalf("TOC 不应包含四级标题：%s", toc)
	}
	if got := TOC(nil); got != "" {
		t.Fatalf("空目录应返回空串，得到 %q", got)
	}
}

func TestTaskListAndFootnotes(t *testing.T) {
	res := render(t, "- [x] 完成\n- [ ] 待办\n")
	if !strings.Contains(string(res.HTML), `type="checkbox"`) {
		t.Fatalf("任务列表未渲染：\n%s", res.HTML)
	}
}
