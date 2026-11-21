package browse

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/index"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

func TestTreeMatchesWalk(t *testing.T) {
	root, c := disk(t), open(t)
	_, err := index.Record(t.Context(), c, root, index.Options{BatchSize: 1000})
	must(t, err)
	var walked []uvsh.Entry
	_, err = index.Walk(t.Context(), root, index.Options{}, func(e uvsh.Entry) error {
		if e.Kind != uvsh.Other {
			walked = append(walked, e)
		}
		return nil
	})
	must(t, err)

	for _, page := range []int{1, 2, 1000} {
		got := collect(t, Tree(t.Context(), c, root, page))
		if !slices.EqualFunc(got, walked, sameEntry) {
			t.Errorf("page %d:\n got %v\nwant %v", page, got, walked)
		}
	}
}

func TestTreeOfAFile(t *testing.T) {
	root, c := disk(t), open(t)
	_, err := index.Record(t.Context(), c, root, index.Options{BatchSize: 1000})
	must(t, err)
	got := collect(t, Tree(t.Context(), c, filepath.Join(root, "a/m.txt"), 10))
	if len(got) != 1 || got[0].Path != "m.txt" || got[0].Kind != uvsh.File {
		t.Errorf("Tree(file) = %v, want just m.txt", got)
	}
}

// Breaking out of a range must stop the iterator cleanly at any point, deep
// in the recursion included: calling yield again would panic.

func TestTreeAndListStopEarly(t *testing.T) {
	root, c := disk(t), open(t)
	_, err := index.Record(t.Context(), c, root, index.Options{BatchSize: 1000})
	must(t, err)
	all := collect(t, Tree(t.Context(), c, root, 1))
	for stop := 1; stop <= len(all); stop++ {
		n := 0
		for _, err := range Tree(t.Context(), c, root, 1) {
			must(t, err)
			if n++; n == stop {
				break
			}
		}
		if n != stop {
			t.Errorf("Tree: stopped after %d entries, want %d", n, stop)
		}
	}
	for range List(t.Context(), c, root, "", 1) {
		break
	}
}

func TestList(t *testing.T) {
	root, c := disk(t), open(t)
	_, err := index.Record(t.Context(), c, root, index.Options{BatchSize: 1000})
	must(t, err)
	names := func(after string, page int) []string {
		var got []string
		for _, e := range collect(t, List(t.Context(), c, root, after, page)) {
			got = append(got, e.Path)
		}
		return got
	}
	want := []string{"B.txt", "a", "b.txt", "dangling", "empty", "new\nline", "\xff.bin"}
	for _, page := range []int{1, 2, len(want), 1000} {
		if got := names("", page); !slices.Equal(got, want) {
			t.Errorf("page %d: got %q, want %q", page, got, want)
		}
	}
	if got := names("dangling", 2); !slices.Equal(got, want[4:]) {
		t.Errorf("after dangling: got %q, want %q", got, want[4:])
	}
	for _, err := range List(t.Context(), c, filepath.Join(root, "b.txt"), "", 10) {
		if !errors.Is(err, catalog.ErrNotDir) {
			t.Errorf("List(file) error = %v, want ErrNotDir", err)
		}
	}
}

func sameEntry(a, b uvsh.Entry) bool {
	return a.Path == b.Path && a.Kind == b.Kind && a.Size == b.Size && a.Target == b.Target && a.ModTime.Equal(b.ModTime)
}
