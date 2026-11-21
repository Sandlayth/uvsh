// Package catalogtest checks that a catalog.Catalog behaves as the interface
// documents, the way testing/fstest checks an fs.FS. Every backend runs it
// from its own tests:
//
//	func TestConformance(t *testing.T) { catalogtest.Run(t, openEmptyCatalog) }
package catalogtest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

// Opener returns a new, empty catalog that is closed when t ends.
type Opener func(t *testing.T) catalog.Catalog

func Run(t *testing.T, open Opener) {
	for _, tt := range []struct {
		name string
		test func(*testing.T, Opener)
	}{
		{"ScanWritesTree", testScanWritesTree},
		{"ScanBatches", testScanBatches},
		{"Rescan", testRescan},
		{"ScanRootsAreSeparate", testScanRootsAreSeparate},
		{"ScanAbortDiscardsOpenBatch", testScanAbortDiscardsOpenBatch},
		{"ScanFinishAfterCancel", testScanFinishAfterCancel},
		{"ScanRejects", testScanRejects},
		{"ScanLinks", testScanLinks},
		{"ScanKindChange", testScanKindChange},
		{"ScanRootOverlap", testScanRootOverlap},
		{"ScanRootOverlapConcurrent", testScanRootOverlapConcurrent},
		{"ListPages", testListPages},
		{"ListInsertBeforeCursor", testListInsertBeforeCursor},
		{"ListRejects", testListRejects},
		{"Stat", testStat},
		{"Roots", testRoots},
		{"ReadSnapshot", testReadSnapshot},
	} {
		t.Run(tt.name, func(t *testing.T) { tt.test(t, open) })
	}
}

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
			entry = uvsh.Entry{Path: name, Kind: uvsh.Symlink, Target: target, ModTime: e.mod}
		} else if n := len(e.path); e.path[n-1] == '/' {
			entry = uvsh.Entry{Path: e.path[:n-1], Kind: uvsh.Dir, ModTime: e.mod}
		}
		if err := w.Add(ctx, entry); err != nil {
			t.Fatalf("add %s: %v", e.path, err)
		}
	}
	stats, err := w.Finish(ctx)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return stats
}

// listAll pages through dir and returns "kind name[ -> target]" for each entry.
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
			if e.Kind == uvsh.Symlink {
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

func testScanWritesTree(t *testing.T, open Opener) {
	c := open(t)
	stats := record(t, c, "/data", 1000, sample)

	want := catalog.ScanStats{Written: map[uvsh.Kind]int{uvsh.Dir: 3, uvsh.File: 4}, Batches: 1}
	if !sameStats(stats, want) {
		t.Errorf("stats = %+v, want %+v", stats, want)
	}
	if e := stat(t, c, "/data/photos/2023/img.jpg"); e.Kind != uvsh.File || e.Size != 100 || !e.ModTime.Equal(t0) {
		t.Errorf("img.jpg = %+v", e)
	}
	if got, want := listAll(t, c, "/data/photos", 1000), []string{"dir 2023", "file z.txt"}; !slices.Equal(got, want) {
		t.Errorf("photos = %q, want %q", got, want)
	}
}

func testScanBatches(t *testing.T, open Opener) {
	c := open(t)
	entries := tree{{"./", 0, t0}}
	for i := range 2499 {
		entries = append(entries, tree{{fmt.Sprintf("f%04d", i), 1, t0}}...)
	}
	// 2500 entries in batches of 1000: 1000 + 1000 + 500.
	if got := record(t, c, "/data", 1000, entries).Batches; got != 3 {
		t.Errorf("Batches = %d, want 3", got)
	}
	if got := len(listAll(t, c, "/data", 1000)); got != 2499 {
		t.Errorf("files = %d, want 2499", got)
	}
}

func testRescan(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 2, sample)

	t.Run("unchanged writes nothing", func(t *testing.T) {
		want := catalog.ScanStats{Unchanged: map[uvsh.Kind]int{uvsh.Dir: 3, uvsh.File: 4}, Batches: 4}
		if got := record(t, c, "/data", 2, sample); !sameStats(got, want) {
			t.Errorf("stats = %+v, want %+v", got, want)
		}
	})

	t.Run("changes are written", func(t *testing.T) {
		changed := append(tree(nil), sample...)
		changed[1].size = 4                  // a.txt grew
		changed[2].mod = t0.Add(time.Second) // photos/ got a new entry
		changed[5].mod = t0.Add(time.Hour)   // photos/z.txt touched
		stats := record(t, c, "/data", 1000, changed)
		if stats.Written[uvsh.File] != 2 || stats.Written[uvsh.Dir] != 1 {
			t.Errorf("stats = %+v, want 2 files and 1 dir written", stats)
		}
		if got := stat(t, c, "/data/a.txt").Size; got != 4 {
			t.Errorf("a.txt size = %d, want 4", got)
		}
	})

	if got := rootPaths(t, c); !slices.Equal(got, []string{"/data"}) {
		t.Errorf("roots = %q, want [/data]", got)
	}
}

func testScanRootsAreSeparate(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/a", 1000, sample)
	record(t, c, "/b", 1000, sample)
	record(t, c, "/a/", 1000, sample) // same root as /a once cleaned

	if got := rootPaths(t, c); !slices.Equal(got, []string{"/a", "/b"}) {
		t.Errorf("roots = %q, want [/a /b]", got)
	}
	for _, root := range []string{"/a", "/b"} {
		if got := len(listAll(t, c, root, 1000)); got != 3 {
			t.Errorf("%s has %d entries, want 3", root, got)
		}
	}
}

func testScanAbortDiscardsOpenBatch(t *testing.T, open Opener) {
	c := open(t)
	ctx := t.Context()
	w, err := c.BeginScan(ctx, "/data", catalog.ScanOptions{BatchSize: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Add(ctx, uvsh.Entry{Path: ".", Kind: uvsh.Dir, ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(ctx, uvsh.Entry{Path: "a", Kind: uvsh.File, Size: 1, ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	w.Abort()
	w.Abort() // idempotent

	if _, err := c.Stat(ctx, "/data/a"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Stat after Abort: error = %v, want ErrNotFound", err)
	}
	if _, err := w.Finish(ctx); err == nil {
		t.Error("Finish after Abort succeeded")
	}
}

// Ctrl-C cancels the walk's ctx; what was walked must still be committable.
func testScanFinishAfterCancel(t *testing.T, open Opener) {
	c := open(t)
	ctx, cancel := context.WithCancel(t.Context())
	w, err := c.BeginScan(ctx, "/data", catalog.ScanOptions{BatchSize: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	if err := w.Add(ctx, uvsh.Entry{Path: ".", Kind: uvsh.Dir, ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(ctx, uvsh.Entry{Path: "a", Kind: uvsh.File, Size: 1, ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	cancel()

	if _, err := w.Finish(context.WithoutCancel(ctx)); err != nil {
		t.Fatalf("Finish after cancel: %v", err)
	}
	stat(t, c, "/data/a")
}

func testScanRejects(t *testing.T, open Opener) {
	c := open(t)
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
	if err := w.Add(ctx, uvsh.Entry{Path: "orphan/f", Kind: uvsh.File, ModTime: t0}); err == nil {
		t.Error("Add without its parent dir succeeded")
	}
	if err := w.Add(ctx, uvsh.Entry{Path: ".", Kind: uvsh.Dir, ModTime: t0}); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(ctx, uvsh.Entry{Path: "fifo", Kind: uvsh.Other, ModTime: t0}); err == nil {
		t.Error("Add of an Other entry succeeded")
	}
}

func testScanLinks(t *testing.T, open Opener) {
	c := open(t)
	links := tree{
		{"./", 0, t0},
		{"a.txt", 1, t0},
		{"dangling -> nope", 0, t0}, // targets are stored, never checked
		{"loop -> loop", 0, t0},
		{"sub/", 0, t0},
		{"sub/up -> ../a.txt", 0, t0},
	}
	if got := record(t, c, "/data", 1000, links); got.Written[uvsh.Symlink] != 3 || got.Unchanged[uvsh.Symlink] != 0 {
		t.Errorf("first scan: stats = %+v, want 3 links written", got)
	}
	if got := record(t, c, "/data", 1000, links); got.Written[uvsh.Symlink] != 0 || got.Unchanged[uvsh.Symlink] != 3 {
		t.Errorf("rescan: stats = %+v, want 3 links unchanged", got)
	}
	changed := append(tree(nil), links...)
	changed[2].path = "dangling -> a.txt" // retargeted
	changed[3].mod = t0.Add(time.Second)  // recreated, same target
	if got := record(t, c, "/data", 1000, changed); got.Written[uvsh.Symlink] != 2 || got.Unchanged[uvsh.Symlink] != 1 {
		t.Errorf("changed: stats = %+v, want 2 links written, 1 unchanged", got)
	}
}

// The rule itself is catalog.CheckRoot; this checks the backend applies it.
func testScanRootOverlap(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, sample)
	for _, root := range []string{"/data/photos", "/"} {
		if w, err := c.BeginScan(t.Context(), root, catalog.ScanOptions{BatchSize: 1000}); !errors.Is(err, catalog.ErrRootOverlap) {
			if err == nil {
				w.Abort()
			}
			t.Errorf("BeginScan(%s) error = %v, want ErrRootOverlap", root, err)
		}
	}
	record(t, c, "/data", 1000, sample) // rescanning the same root is fine
	if got := rootPaths(t, c); !slices.Equal(got, []string{"/data"}) {
		t.Errorf("roots = %q after refused scans, want [/data]", got)
	}
}

// Check and insert must be atomic: of two overlapping roots registered at
// once, exactly one wins.
func testScanRootOverlapConcurrent(t *testing.T, open Opener) {
	for range 20 {
		c := open(t)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, root := range []string{"/data", "/data/photos"} {
			wg.Go(func() {
				w, err := c.BeginScan(t.Context(), root, catalog.ScanOptions{BatchSize: 1000})
				if err == nil {
					w.Abort()
				}
				errs[i] = err
			})
		}
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("errors = %v, want exactly one scan refused", errs)
		}
		for _, err := range errs {
			if err != nil && !errors.Is(err, catalog.ErrRootOverlap) {
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if got := rootPaths(t, c); len(got) != 1 {
			t.Fatalf("roots = %q, want exactly one", got)
		}
	}
}

func testListPages(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, tree{
		{"./", 0, t0},
		{"B", 1, t0},
		{"a", 1, t0},
		{"l -> x/deep", 0, t0},
		{"x/", 0, t0},
		{"x/deep", 1, t0},
		{"x.txt", 1, t0},
		{"\xff", 1, t0},   // invalid UTF-8 sorts last, by bytes
		{"\u00e9", 1, t0}, // é is 0xC3 0xA9: after ASCII, before 0xFF
		{"line\nbreak", 1, t0},
	})
	want := []string{"file B", "file a", "link l -> x/deep", "file line\nbreak", "dir x", "file x.txt", "file \u00e9", "file \xff"}

	for _, page := range []int{1, 2, 3, len(want), len(want) + 1, 1000} {
		if got := listAll(t, c, "/data", page); !slices.Equal(got, want) {
			t.Errorf("page %d:\n got %q\nwant %q", page, got, want)
		}
	}
	if got := listAll(t, c, "/data/x", 10); !slices.Equal(got, []string{"file deep"}) {
		t.Errorf("/data/x = %q", got)
	}
}

// A name holds one entry, as on disk: when a rescan finds another kind under
// it, the entry becomes that kind.
func testScanKindChange(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, tree{{"./", 0, t0}, {"a", 1, t0}, {"b/", 0, t0}, {"c", 1, t0}})
	stats := record(t, c, "/data", 1000, tree{{"./", 0, t0}, {"a/", 0, t0}, {"b", 7, t0}, {"c -> a", 0, t0}})
	if want := map[uvsh.Kind]int{uvsh.Dir: 1, uvsh.File: 1, uvsh.Symlink: 1}; !maps.Equal(stats.Written, want) {
		t.Errorf("stats = %+v, want 1 dir, 1 file and 1 link written", stats)
	}
	want := []string{"dir a", "file b", "link c -> a"}
	if got := listAll(t, c, "/data", 10); !slices.Equal(got, want) {
		t.Errorf("after kind changes: got %q, want %q", got, want)
	}
	if e := stat(t, c, "/data/b"); e.Size != 7 {
		t.Errorf("b size = %d, want 7", e.Size)
	}
}

// A row inserted before the cursor between two pages is neither repeated
// nor allowed to shift the next page.
func testListInsertBeforeCursor(t *testing.T, open Opener) {
	c := open(t)
	first := tree{{"./", 0, t0}, {"b", 1, t0}, {"c", 1, t0}, {"d", 1, t0}}
	record(t, c, "/data", 1000, first)

	page1, err := c.List(t.Context(), "/data", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	record(t, c, "/data", 1000, append(first, tree{{"a", 1, t0}}...))
	page2, err := c.List(t.Context(), "/data", page1[len(page1)-1].Path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].Path != "d" {
		t.Errorf("page 2 = %+v, want just d", page2)
	}
}

func testListRejects(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, sample)
	ctx := t.Context()

	if _, err := c.List(ctx, "/data/a.txt", "", 10); !errors.Is(err, catalog.ErrNotDir) {
		t.Errorf("List(file) error = %v, want ErrNotDir", err)
	}
	if _, err := c.List(ctx, "/data/nope", "", 10); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("List(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := c.List(ctx, "data", "", 10); err == nil {
		t.Error("List(relative path) succeeded")
	}
	if _, err := c.List(ctx, "/data", "", 0); err == nil {
		t.Error("List(limit 0) succeeded")
	}
}

func testStat(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, sample)
	record(t, c, "/srv/media", 1000, tree{{"./", 0, t0}, {"top", 9, t0}, {"film -> /data/photos", 0, t0}})

	for _, tt := range []struct {
		path string
		want uvsh.Entry
		err  error
	}{
		{"/data", uvsh.Entry{Path: "data", Kind: uvsh.Dir, ModTime: t0}, nil},
		{"/data/", uvsh.Entry{Path: "data", Kind: uvsh.Dir, ModTime: t0}, nil},
		{"/data/x/../photos", uvsh.Entry{Path: "photos", Kind: uvsh.Dir, ModTime: t0}, nil},
		{"/data/photos/2023", uvsh.Entry{Path: "2023", Kind: uvsh.Dir, ModTime: t0}, nil},
		{"/data/a.txt", uvsh.Entry{Path: "a.txt", Kind: uvsh.File, Size: 3, ModTime: t0}, nil},
		{"/srv/media/top", uvsh.Entry{Path: "top", Kind: uvsh.File, Size: 9, ModTime: t0}, nil},
		{"/srv/media/film", uvsh.Entry{Path: "film", Kind: uvsh.Symlink, Target: "/data/photos", ModTime: t0}, nil},
		{"/srv/media/film/2023", uvsh.Entry{}, catalog.ErrNotFound}, // links are never followed
		{"/srv", uvsh.Entry{}, catalog.ErrNotFound},                 // above a root
		{"/data/nope", uvsh.Entry{}, catalog.ErrNotFound},
		{"/data/a.txt/below", uvsh.Entry{}, catalog.ErrNotFound},
		{"/elsewhere/x", uvsh.Entry{}, catalog.ErrNotFound},
	} {
		got, err := c.Stat(t.Context(), tt.path)
		if !errors.Is(err, tt.err) {
			t.Errorf("Stat(%q) error = %v, want %v", tt.path, err, tt.err)
			continue
		}
		if !got.ModTime.Equal(tt.want.ModTime) {
			t.Errorf("Stat(%q) mtime = %v, want %v", tt.path, got.ModTime, tt.want.ModTime)
		}
		got.ModTime, tt.want.ModTime = time.Time{}, time.Time{}
		if got != tt.want {
			t.Errorf("Stat(%q) = %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

// Lookups inside one Read don't see a scan that commits while it runs.
func testReadSnapshot(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, sample)
	child := func(l catalog.Lookup, name string) bool {
		t.Helper()
		top, ok, err := l.Root(t.Context(), "/data")
		if err != nil || !ok {
			t.Fatalf("Root(/data) = %v, %v", ok, err)
		}
		_, ok, err = l.Child(t.Context(), top, name)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	err := c.Read(t.Context(), func(l catalog.Lookup) error {
		if !child(l, "a.txt") {
			t.Error("a.txt not found in the snapshot")
		}
		record(t, c, "/data", 1000, append(sample, tree{{"new.txt", 1, t0}}...))
		if child(l, "new.txt") {
			t.Error("Read saw a scan committed after its first lookup")
		}
		return nil
	})
	must(t, err)
	stat(t, c, "/data/new.txt") // visible to the next read

	boom := errors.New("boom")
	if err := c.Read(t.Context(), func(catalog.Lookup) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("Read error = %v, want fn's error", err)
	}
}

func testRoots(t *testing.T, open Opener) {
	c := open(t)
	if got := rootPaths(t, c); len(got) != 0 {
		t.Fatalf("empty catalog: roots = %q", got)
	}
	record(t, c, "/b", 1000, sample)
	record(t, c, "/a", 1000, sample)
	if got := rootPaths(t, c); !slices.Equal(got, []string{"/a", "/b"}) {
		t.Errorf("roots = %q, want [/a /b] in path order", got)
	}
}
