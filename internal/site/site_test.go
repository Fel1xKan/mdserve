package site

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testMount(t *testing.T) *Mount {
	t.Helper()
	root := t.TempDir()
	write(t, root, "README.md", "# 首页\n")
	write(t, root, "notes.md", "# 笔记\n")
	write(t, root, "guide/README.md", "# 指南\n")
	write(t, root, "guide/a.md", "# A\n")
	write(t, root, "img/logo.png", "PNG")

	m := NewMount("docs", "", root, Options{AllowExt: []string{".png"}})
	if err := m.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return m
}

func TestResolve(t *testing.T) {
	m := testMount(t)
	cases := []struct {
		in   string
		kind Kind
		path string
	}{
		{"", KindDoc, "README.md"},
		{"notes.md", KindDoc, "notes.md"},
		{"notes", KindDoc, "notes.md"},
		{"guide", KindDoc, "guide/README.md"},
		{"guide/", KindDoc, "guide/README.md"},
		{"guide/a", KindDoc, "guide/a.md"},
		{"img/logo.png", KindAsset, "img/logo.png"},
		{"missing", KindMissing, ""},
		{"../etc/passwd", KindMissing, ""},
		{"guide/../../etc/passwd", KindMissing, ""},
	}
	for _, c := range cases {
		got := m.Resolve(c.in)
		if got.Kind != c.kind || (c.path != "" && got.Path != c.path) {
			t.Errorf("Resolve(%q) = {%v %q %q}, want {%v %q}",
				c.in, got.Kind, got.Path, got.Dir, c.kind, c.path)
		}
	}
}

func TestResolveDirKeepsDirForRedirect(t *testing.T) {
	m := testMount(t)
	got := m.Resolve("guide")
	if got.Dir != "guide" {
		t.Fatalf("Dir = %q, want guide", got.Dir)
	}
}

func TestAssetPathRejectsEscape(t *testing.T) {
	m := testMount(t)
	if _, err := m.AssetPath("../../etc/passwd"); err == nil {
		t.Fatal("AssetPath 允许了越界路径")
	}
	if _, err := m.AssetPath("img/logo.png"); err != nil {
		t.Fatalf("正常资源被拒绝: %v", err)
	}
}

func TestAssetPathRejectsSymlinkEscape(t *testing.T) {
	m := testMount(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.png")
	if err := os.WriteFile(secret, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(m.Root, "img", "link.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, err := m.AssetPath("img/link.png"); err == nil {
		t.Fatal("AssetPath 允许了符号链接越界")
	}
}

func TestTreeHidesNothingButSortsDirsFirst(t *testing.T) {
	m := testMount(t)
	tree := m.Tree()
	if len(tree) == 0 {
		t.Fatal("目录树为空")
	}
	if tree[0].Type != "dir" {
		t.Fatalf("第一个节点应为目录，实际 %q", tree[0].Type)
	}
}

func TestSearch(t *testing.T) {
	m := testMount(t)
	write(t, m.Root, "guide/a.md", "# A\n\n这里有 zebra 关键词\n")
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	results := m.Search("zebra", 10)
	if len(results) != 1 || results[0].Path != "guide/a.md" {
		t.Fatalf("Search 结果 = %+v", results)
	}
	if results[0].Hits[0].Line != 3 {
		t.Fatalf("行号 = %d, want 3", results[0].Hits[0].Line)
	}
	if got := m.Search("z", 10); got != nil {
		t.Fatalf("单字符检索应返回空，得到 %+v", got)
	}
}
