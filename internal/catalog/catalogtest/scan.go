package catalogtest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

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
	w := begin(t, c, "/data")
	must(t, w.Add(ctx, uvsh.Entry{Path: "a", Kind: uvsh.File, Size: 1, ModTime: t0}))
	w.Abort()
	w.Abort() // idempotent

	if _, err := c.Stat(ctx, "/data/a"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Stat after Abort: error = %v, want ErrNotFound", err)
	}
	if _, err := w.Finish(ctx, true); err == nil {
		t.Error("Finish after Abort succeeded")
	}
	record(t, c, "/data", 1000, sample) // Abort ended the scan: the next one can begin
}

// Ctrl-C cancels the walk's ctx; what was walked must still be committable.

func testScanFinishAfterCancel(t *testing.T, open Opener) {
	c := open(t)
	ctx, cancel := context.WithCancel(t.Context())
	w := begin(t, c, "/data")
	must(t, w.Add(ctx, uvsh.Entry{Path: "a", Kind: uvsh.File, Size: 1, ModTime: t0}))
	cancel()

	if _, err := w.Finish(context.WithoutCancel(ctx), false); err != nil {
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

	w := begin(t, c, "/data")
	if err := w.Add(ctx, uvsh.Entry{Path: "orphan/f", Kind: uvsh.File, ModTime: t0}); err == nil {
		t.Error("Add without its parent dir succeeded")
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
	if got := record(t, c, "/data", 1000, links); got.Written[uvsh.Link] != 3 || got.Unchanged[uvsh.Link] != 0 {
		t.Errorf("first scan: stats = %+v, want 3 links written", got)
	}
	if got := record(t, c, "/data", 1000, links); got.Written[uvsh.Link] != 0 || got.Unchanged[uvsh.Link] != 3 {
		t.Errorf("rescan: stats = %+v, want 3 links unchanged", got)
	}
	changed := append(tree(nil), links...)
	changed[2].path = "dangling -> a.txt" // retargeted
	changed[3].mod = t0.Add(time.Second)  // recreated, same target
	if got := record(t, c, "/data", 1000, changed); got.Written[uvsh.Link] != 2 || got.Unchanged[uvsh.Link] != 1 {
		t.Errorf("changed: stats = %+v, want 2 links written, 1 unchanged", got)
	}
}

// The rule itself is catalog.CheckRoot; this checks the backend applies it.

func testScanKindChange(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, tree{{"./", 0, t0}, {"a", 1, t0}, {"b/", 0, t0}, {"b/in", 1, t0}, {"c", 1, t0}})
	stats := record(t, c, "/data", 1000, tree{{"./", 0, t0}, {"a/", 0, t0}, {"b", 7, t0}, {"c -> a", 0, t0}})
	if want := map[uvsh.Kind]int{uvsh.Dir: 1, uvsh.File: 1, uvsh.Link: 1}; !maps.Equal(stats.Written, want) || stats.Missing != 1 {
		t.Errorf("stats = %+v, want 1 dir, 1 file and 1 link written, b/in missing", stats)
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
