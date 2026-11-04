package hoststate

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "nope.json"))
	if len(s.Hosts) != 0 || !s.Updated.IsZero() {
		t.Fatalf("缺失文件应返回零值，实际 %+v", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "hosts.json")
	if err := Save(path, []string{"B.example.com", "a.example.com", "a.example.com", ""}); err != nil {
		t.Fatal(err)
	}
	s := Load(path)
	want := []string{"a.example.com", "b.example.com"}
	if !reflect.DeepEqual(s.Hosts, want) {
		t.Fatalf("hosts = %v，想要 %v", s.Hosts, want)
	}
	if s.Updated.IsZero() {
		t.Fatal("Updated 不应为零值")
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Fatalf("权限 = %04o，想要 0600", perm)
		}
	}
}

func TestLoadCorruptIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := Load(path); len(s.Hosts) != 0 {
		t.Fatalf("损坏文件应返回零值，实际 %+v", s)
	}
}

func TestRemoved(t *testing.T) {
	cases := []struct {
		prev, cur, want []string
	}{
		{[]string{"a", "b"}, []string{"b", "c"}, []string{"a"}},
		{[]string{"a"}, []string{"a"}, nil},
		{nil, []string{"a"}, nil},
		{[]string{"A."}, nil, []string{"a"}},
		{[]string{"b", "a"}, []string{"c"}, []string{"a", "b"}},
	}
	for _, c := range cases {
		if got := Removed(c.prev, c.cur); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Removed(%v, %v) = %v，想要 %v", c.prev, c.cur, got, c.want)
		}
	}
}
