package catalogtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

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
		{"/srv/media/film", uvsh.Entry{Path: "film", Kind: uvsh.Link, Target: "/data/photos", ModTime: t0}, nil},
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

// Entries a complete scan doesn't see are hidden, not deleted, and come back
// when a later scan sees them again.

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
