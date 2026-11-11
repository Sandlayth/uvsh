package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
)

var t0 = time.Date(2023, 1, 15, 12, 30, 0, 0, time.UTC)

// tree is a walk in depth-first order: dirs end with "/", the rest are files.
type tree []struct {
	path string
	size int64
	mod  time.Time
}

func record(t *testing.T, c *Catalog, root string, batch int, entries tree) catalog.ScanStats {
	t.Helper()
	ctx := t.Context()
	w, err := c.BeginScan(ctx, root, catalog.ScanOptions{BatchSize: batch})
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	defer w.Abort()
	for _, e := range entries {
		if n := len(e.path); e.path[n-1] == '/' {
			err = w.AddDir(ctx, catalog.DirRecord{Path: e.path[:n-1], ModTime: e.mod})
		} else {
			err = w.AddFile(ctx, catalog.FileRecord{Path: e.path, Size: e.size, ModTime: e.mod})
		}
		if err != nil {
			t.Fatalf("add %s: %v", e.path, err)
		}
	}
	stats, err := w.Finish(ctx)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return stats
}

func count(t *testing.T, c *Catalog, query string, args ...any) int {
	t.Helper()
	var n int
	if err := c.reader.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
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

func TestScanWritesTree(t *testing.T) {
	c, _ := openTest(t)
	stats := record(t, c, "/data", 1000, sample)

	want := catalog.ScanStats{DirsWritten: 3, FilesWritten: 4, Batches: 1}
	if stats != want {
		t.Errorf("stats = %+v, want %+v", stats, want)
	}
	// photos/2023/img.jpg sits under root -> photos -> 2023.
	n := count(t, c, `SELECT count(*) FROM files f
		JOIN dirs d2023 ON f.dir_id = d2023.id AND d2023.name = '2023'
		JOIN dirs dphotos ON d2023.parent_id = dphotos.id AND dphotos.name = 'photos'
		JOIN dirs droot ON dphotos.parent_id = droot.id AND droot.parent_id IS NULL
		WHERE f.name = 'img.jpg' AND f.size = 100`)
	if n != 1 {
		t.Errorf("img.jpg not found at photos/2023 under the root dir")
	}
}

func TestScanBatches(t *testing.T) {
	c, _ := openTest(t)
	entries := tree{{"./", 0, t0}}
	for i := range 2499 {
		entries = append(entries, struct {
			path string
			size int64
			mod  time.Time
		}{fmt.Sprintf("f%04d", i), 1, t0})
	}
	// 2500 entries in batches of 1000: 1000 + 1000 + 500.
	if got := record(t, c, "/data", 1000, entries).Batches; got != 3 {
		t.Errorf("Batches = %d, want 3", got)
	}
	if got := count(t, c, `SELECT count(*) FROM files`); got != 2499 {
		t.Errorf("files = %d, want 2499", got)
	}
}

func TestRescan(t *testing.T) {
	c, _ := openTest(t)
	record(t, c, "/data", 2, sample)

	t.Run("unchanged writes nothing", func(t *testing.T) {
		want := catalog.ScanStats{DirsUnchanged: 3, FilesUnchanged: 4, Batches: 4}
		if got := record(t, c, "/data", 2, sample); got != want {
			t.Errorf("stats = %+v, want %+v", got, want)
		}
	})

	t.Run("changes are written", func(t *testing.T) {
		changed := append(tree(nil), sample...)
		changed[1].size = 4                  // a.txt grew
		changed[2].mod = t0.Add(time.Second) // photos/ got a new entry
		changed[5].mod = t0.Add(time.Hour)   // photos/z.txt touched
		stats := record(t, c, "/data", 1000, changed)
		if stats.FilesWritten != 2 || stats.DirsWritten != 1 {
			t.Errorf("stats = %+v, want 2 files and 1 dir written", stats)
		}
		if got := count(t, c, `SELECT size FROM files WHERE name = 'a.txt'`); got != 4 {
			t.Errorf("a.txt size = %d, want 4", got)
		}
	})

	if got := count(t, c, `SELECT count(*) FROM roots`); got != 1 {
		t.Errorf("roots = %d, want 1", got)
	}
}

func TestScanRootsAreSeparate(t *testing.T) {
	c, _ := openTest(t)
	record(t, c, "/a", 1000, sample)
	record(t, c, "/b", 1000, sample)
	record(t, c, "/a/", 1000, sample) // same root as /a once cleaned

	if got := count(t, c, `SELECT count(*) FROM roots`); got != 2 {
		t.Errorf("roots = %d, want 2", got)
	}
	if got := count(t, c, `SELECT count(*) FROM files`); got != 2*4 {
		t.Errorf("files = %d, want 8", got)
	}
}

func TestScanAbortDiscardsOpenBatch(t *testing.T) {
	c, _ := openTest(t)
	ctx := t.Context()
	w, err := c.BeginScan(ctx, "/data", catalog.ScanOptions{BatchSize: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddDir(ctx, catalog.DirRecord{Path: ".", ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddFile(ctx, catalog.FileRecord{Path: "a", Size: 1, ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	w.Abort()
	w.Abort() // idempotent

	if got := count(t, c, `SELECT count(*) FROM files`); got != 0 {
		t.Errorf("files = %d after Abort, want 0", got)
	}
	if _, err := w.Finish(ctx); err == nil {
		t.Error("Finish after Abort succeeded")
	}
}

// Ctrl-C cancels the walk's ctx; what was walked must still be committable.
func TestScanFinishAfterCancel(t *testing.T) {
	c, _ := openTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	w, err := c.BeginScan(ctx, "/data", catalog.ScanOptions{BatchSize: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	if err := w.AddDir(ctx, catalog.DirRecord{Path: ".", ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddFile(ctx, catalog.FileRecord{Path: "a", Size: 1, ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	cancel()

	if _, err := w.Finish(context.WithoutCancel(ctx)); err != nil {
		t.Fatalf("Finish after cancel: %v", err)
	}
	if got := count(t, c, `SELECT count(*) FROM files`); got != 1 {
		t.Errorf("files = %d, want 1", got)
	}
}

func TestScanRejects(t *testing.T) {
	c, _ := openTest(t)
	ctx := t.Context()

	for _, tt := range []struct {
		name  string
		root  string
		batch int
	}{
		{"zero batch", "/data", 0},
		{"relative root", "data", 1000},
	} {
		if w, err := c.BeginScan(ctx, tt.root, catalog.ScanOptions{BatchSize: tt.batch}); err == nil {
			w.Abort()
			t.Errorf("%s: BeginScan succeeded", tt.name)
		}
	}

	w, err := c.BeginScan(ctx, "/data", catalog.ScanOptions{BatchSize: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	if err := w.AddFile(ctx, catalog.FileRecord{Path: "orphan/f", ModTime: t0}); err == nil {
		t.Error("AddFile without its parent dir succeeded")
	}
}
