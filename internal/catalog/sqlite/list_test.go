package sqlite

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
)

// listAll pages through dir with the given page size.
func listAll(t *testing.T, c *Catalog, dir string, page int) []string {
	t.Helper()
	var got []string
	var after catalog.Cursor
	for {
		entries, err := c.List(t.Context(), dir, after, page)
		if err != nil {
			t.Fatalf("List(%s, page %d): %v", dir, page, err)
		}
		for _, e := range entries {
			got = append(got, e.Kind.String()+" "+e.Name)
		}
		if len(entries) < page {
			return got
		}
		after = entries[len(entries)-1].Cursor()
	}
}

func TestListPages(t *testing.T) {
	c, _ := openTest(t)
	record(t, c, "/data", 1000, tree{
		{"./", 0, t0},
		{"B", 1, t0},
		{"a", 1, t0},
		{"x/", 0, t0},
		{"x/deep", 1, t0},
		{"x", 1, t0},      // a file and a dir may share a name in the catalog
		{"\xff", 1, t0},   // invalid UTF-8 sorts last, by bytes
		{"\u00e9", 1, t0}, // é is 0xC3 0xA9: after ASCII, before 0xFF
		{"line\nbreak", 1, t0},
	})
	want := []string{"file B", "file a", "file line\nbreak", "dir x", "file x", "file \u00e9", "file \xff"}

	for _, page := range []int{1, 2, 3, len(want), len(want) + 1, 1000} {
		if got := listAll(t, c, "/data", page); !slices.Equal(got, want) {
			t.Errorf("page %d:\n got %q\nwant %q", page, got, want)
		}
	}
	if got := listAll(t, c, "/data/x", 10); !slices.Equal(got, []string{"file deep"}) {
		t.Errorf("/data/x = %q", got)
	}
}

// A row inserted before the cursor between two pages is neither repeated
// nor allowed to shift the next page.
func TestListInsertBeforeCursor(t *testing.T) {
	c, _ := openTest(t)
	first := tree{{"./", 0, t0}, {"b", 1, t0}, {"c", 1, t0}, {"d", 1, t0}}
	record(t, c, "/data", 1000, first)

	page1, err := c.List(t.Context(), "/data", catalog.Cursor{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	record(t, c, "/data", 1000, append(first, tree{{"a", 1, t0}}...))
	page2, err := c.List(t.Context(), "/data", page1[len(page1)-1].Cursor(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].Name != "d" {
		t.Errorf("page 2 = %+v, want just d", page2)
	}
}

func TestStat(t *testing.T) {
	c, _ := openTest(t)
	record(t, c, "/", 1000, tree{{"./", 0, t0}, {"top", 9, t0}})
	record(t, c, "/data", 1000, sample)
	record(t, c, "/data/photos", 1000, tree{{"./", 0, t0}, {"only-here.jpg", 7, t0}})

	for _, tt := range []struct {
		path string
		want catalog.Entry
		err  error
	}{
		{"/data", catalog.Entry{Name: "data", Kind: catalog.Dir, ModTime: t0}, nil},
		{"/data/", catalog.Entry{Name: "data", Kind: catalog.Dir, ModTime: t0}, nil},
		{"/data/x/../photos", catalog.Entry{Name: "photos", Kind: catalog.Dir, ModTime: t0}, nil},
		{"/data/a.txt", catalog.Entry{Name: "a.txt", Kind: catalog.File, Size: 3, ModTime: t0}, nil},
		// The most specific root wins: /data recorded photos/ too, without this file.
		{"/data/photos/only-here.jpg", catalog.Entry{Name: "only-here.jpg", Kind: catalog.File, Size: 7, ModTime: t0}, nil},
		{"/top", catalog.Entry{Name: "top", Kind: catalog.File, Size: 9, ModTime: t0}, nil},
		{"/data/nope", catalog.Entry{}, catalog.ErrNotFound},
		// No fallback to /data, which did record photos/2023.
		{"/data/photos/2023", catalog.Entry{}, catalog.ErrNotFound},
		{"/data/a.txt/below", catalog.Entry{}, catalog.ErrNotFound},
		{"/elsewhere/x", catalog.Entry{}, catalog.ErrNotFound},
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

func TestListRejects(t *testing.T) {
	c, _ := openTest(t)
	record(t, c, "/data", 1000, sample)
	ctx := t.Context()

	if _, err := c.List(ctx, "/data/a.txt", catalog.Cursor{}, 10); !errors.Is(err, catalog.ErrNotDir) {
		t.Errorf("List(file) error = %v, want ErrNotDir", err)
	}
	if _, err := c.List(ctx, "/data/nope", catalog.Cursor{}, 10); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("List(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := c.List(ctx, "data", catalog.Cursor{}, 10); err == nil {
		t.Error("List(relative path) succeeded")
	}
	if _, err := c.List(ctx, "/data", catalog.Cursor{}, 0); err == nil {
		t.Error("List(limit 0) succeeded")
	}
}

func TestRoots(t *testing.T) {
	c, _ := openTest(t)
	if roots, err := c.Roots(t.Context()); err != nil || len(roots) != 0 {
		t.Fatalf("empty catalog: Roots = %v, %v", roots, err)
	}
	record(t, c, "/b", 1000, sample)
	record(t, c, "/a", 1000, sample)
	roots, err := c.Roots(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range roots {
		paths = append(paths, r.Path)
	}
	if !slices.Equal(paths, []string{"/a", "/b"}) {
		t.Errorf("roots = %q, want [/a /b]", paths)
	}
}
