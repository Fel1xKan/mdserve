package site

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SearchHit 是一条命中：行号 + 该行文本。
type SearchHit struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchResult 是一篇命中的文档。
type SearchResult struct {
	Path  string      `json:"path"`
	Title string      `json:"title"`
	Hits  []SearchHit `json:"hits"`
}

// 单文件超过这个大小就不参与检索，避免把整个仓库读进内存。
const searchMaxFile = 4 << 20

// Search 在挂载点内做大小写不敏感的子串检索。
// 每篇文档最多保留 3 条命中，整体最多 limit 篇。
func (m *Mount) Search(q string, limit int) []SearchResult {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	needle := strings.ToLower(q)

	m.mu.RLock()
	snapshot := make([]*Node, 0, len(m.files))
	for _, n := range m.files {
		snapshot = append(snapshot, n)
	}
	m.mu.RUnlock()
	sort.Slice(snapshot, func(i, j int) bool { return naturalLess(snapshot[i].Path, snapshot[j].Path) })

	out := make([]SearchResult, 0, limit)
	for _, n := range snapshot {
		if n.Size > searchMaxFile {
			continue
		}
		abs := filepath.Join(m.Root, filepath.FromSlash(n.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		if !strings.Contains(strings.ToLower(text), needle) {
			continue
		}
		res := SearchResult{Path: n.Path, Title: n.Title}
		for i, line := range strings.Split(text, "\n") {
			if len(res.Hits) >= 3 {
				break
			}
			if strings.Contains(strings.ToLower(line), needle) {
				res.Hits = append(res.Hits, SearchHit{
					Line: i + 1,
					Text: strings.TrimSpace(truncateRunes(line, 240)),
				})
			}
		}
		if len(res.Hits) == 0 {
			// 命中在超长行之外（例如被截断），仍然给出结果。
			res.Hits = append(res.Hits, SearchHit{Line: 1, Text: strings.TrimSpace(truncateRunes(text, 200))})
		}
		out = append(out, res)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
