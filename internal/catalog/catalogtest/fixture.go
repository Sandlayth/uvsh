package catalogtest

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

var t0 = time.Date(2023, 1, 15, 12, 30, 0, 0, time.UTC)

// tree is a walk in depth-first order: dirs end with "/", links are
// "name -> target", the rest are files.

type tree []struct {
	path string
	size int64
	mod  time.Time
}

var sample = tree{
	{"./", 0, t0},
	{"a.txt", 3, t0},
	{"photos/", 0, t0},
	{"photos/2023/", 0, t0},
	{"photos/2023/img.jpg", 100, t0},
	{"photos/z.txt", 1, t0}, // back up one level after a deeper dir
	{"zz.txt", 2, t0},       // back up to the root
}

func record(t *testing.T, c catalog.Catalog, root string, batch int, entries tree) catalog.ScanStats {
	t.Helper()
	ctx := t.Context()
	w, err := c.BeginScan(ctx, root, catalog.ScanOptions{BatchSize: batch})
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	defer w.Abort()
	for _, e := range entries {
		entry := uvsh.Entry{Path: e.path, Kind: uvsh.File, Size: e.size, ModTime: e.mod}
		if name, target, ok := strings.Cut(e.path, " -> "); ok {
			entry = uvsh.Entry{Path: name, Kind: uvsh.Link, Target: target, ModTime: e.mod}
		} else if n := len(e.path); e.path[n-1] == '/' {
			entry = uvsh.Entry{Path: e.path[:n-1], Kind: uvsh.Dir, ModTime: e.mod}
		}
		if err := w.Add(ctx, entry); err != nil {
			t.Fatalf("add %s: %v", e.path, err)
		}
	}
	stats, err := w.Finish(ctx, true)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return stats
}

// listAll pages through dir and returns "kind name[ -> target]" for each entry.

// begin opens a scan of root and adds its top dir. The scan is aborted when
// t ends, unless finished before.
func begin(t *testing.T, c catalog.Catalog, root string) catalog.ScanWriter {
	t.Helper()
	w, err := c.BeginScan(t.Context(), root, catalog.ScanOptions{BatchSize: 1000})
	must(t, err)
	t.Cleanup(w.Abort)
	must(t, w.Add(t.Context(), uvsh.Entry{Path: ".", Kind: uvsh.Dir, ModTime: t0}))
	return w
}

func listAll(t *testing.T, c catalog.Catalog, dir string, page int) []string {
	t.Helper()
	var got []string
	var after string
	for {
		entries, err := c.List(t.Context(), dir, after, page)
		if err != nil {
			t.Fatalf("List(%s, page %d): %v", dir, page, err)
		}
		for _, e := range entries {
			s := string(e.Kind) + " " + e.Path
			if e.Kind == uvsh.Link {
				s += " -> " + e.Target
			}
			got = append(got, s)
		}
		if len(entries) < page {
			return got
		}
		after = entries[len(entries)-1].Path
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sameStats(a, b catalog.ScanStats) bool {
	return maps.Equal(a.Written, b.Written) && maps.Equal(a.Unchanged, b.Unchanged) && a.Batches == b.Batches
}

func stat(t *testing.T, c catalog.Catalog, path string) uvsh.Entry {
	t.Helper()
	e, err := c.Stat(t.Context(), path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	return e
}

func rootPaths(t *testing.T, c catalog.Catalog) []string {
	t.Helper()
	roots, err := c.Roots(t.Context())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	var paths []string
	for _, r := range roots {
		paths = append(paths, r.Path)
	}
	return paths
}
