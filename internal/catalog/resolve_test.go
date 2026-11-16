package catalog

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

// fakeLookup is a tree in memory: roots maps a root path to its dir's ID and
// children maps a dir ID to its entries by name. A child named "boom" fails.
type fakeLookup struct {
	roots    map[string]int64
	children map[int64]map[string]Node
}

// newFake builds roots from relative paths; dirs end with "/".
func newFake(roots map[string][]string) *fakeLookup {
	f := &fakeLookup{roots: map[string]int64{}, children: map[int64]map[string]Node{}}
	var next int64
	add := func(parent int64, name string, kind Kind) int64 {
		if n, ok := f.children[parent][name]; ok {
			return n.ID
		}
		next++
		if f.children[parent] == nil {
			f.children[parent] = map[string]Node{}
		}
		f.children[parent][name] = Node{Entry: Entry{Name: name, Kind: kind}, ID: next}
		return next
	}
	for root, entries := range roots {
		next++
		f.roots[root] = next
		for _, e := range entries {
			dir := f.roots[root]
			parts := strings.Split(strings.TrimSuffix(e, "/"), "/")
			for i, name := range parts {
				kind := Dir
				if i == len(parts)-1 && !strings.HasSuffix(e, "/") {
					kind = File
				}
				dir = add(dir, name, kind)
			}
		}
	}
	return f
}

func (f *fakeLookup) Root(_ context.Context, p string) (Node, bool, error) {
	id, ok := f.roots[p]
	return Node{Entry: Entry{Kind: Dir}, ID: id}, ok, nil
}

func (f *fakeLookup) Child(_ context.Context, dir Node, name string) (Node, bool, error) {
	if name == "boom" {
		return Node{}, false, errBoom
	}
	n, ok := f.children[dir.ID][name]
	return n, ok, nil
}

func TestResolve(t *testing.T) {
	l := newFake(map[string][]string{
		"/":            {"top"},
		"/data":        {"a.txt", "photos/2023/img.jpg", "boom"},
		"/data/photos": {"only-here.jpg"},
	})
	for _, tt := range []struct {
		path     string
		wantName string
		wantKind Kind
		wantErr  error
	}{
		{"/data", "data", Dir, nil},
		{"/data/", "data", Dir, nil},
		{"/data/x/../a.txt", "a.txt", File, nil},
		{"/data/a.txt", "a.txt", File, nil},
		{"/top", "top", File, nil},
		{"/", "/", Dir, nil},
		// The most specific root wins, with no fallback to /data.
		{"/data/photos/only-here.jpg", "only-here.jpg", File, nil},
		{"/data/photos/2023", "", 0, ErrNotFound},
		{"/data/nope", "", 0, ErrNotFound},
		{"/data/a.txt/below", "", 0, ErrNotFound},
		{"/elsewhere/x", "", 0, ErrNotFound},
		{"/data/boom", "", 0, errBoom},
	} {
		n, err := Resolve(t.Context(), l, tt.path)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("Resolve(%q) error = %v, want %v", tt.path, err, tt.wantErr)
			continue
		}
		if err == nil && (n.Name != tt.wantName || n.Kind != tt.wantKind) {
			t.Errorf("Resolve(%q) = %s %q, want %s %q", tt.path, n.Kind, n.Name, tt.wantKind, tt.wantName)
		}
	}

	if _, err := Resolve(t.Context(), l, path.Join("data", "a.txt")); err == nil {
		t.Error("Resolve(relative path) succeeded")
	}
}
