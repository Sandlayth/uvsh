package catalogtest

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

func testMissing(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, sample)
	var smaller tree
	for _, e := range sample {
		if !strings.HasPrefix(e.path, "photos/2023/") && e.path != "zz.txt" {
			smaller = append(smaller, e)
		}
	}
	if got := record(t, c, "/data", 1000, smaller); got.Missing != 3 {
		t.Errorf("first scan without them: Missing = %d, want 3 (2023/, img.jpg, zz.txt)", got.Missing)
	}
	for _, p := range []string{"/data/zz.txt", "/data/photos/2023", "/data/photos/2023/img.jpg"} {
		if _, err := c.Stat(t.Context(), p); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("Stat(%s) error = %v, want ErrNotFound", p, err)
		}
	}
	if got, want := listAll(t, c, "/data/photos", 10), []string{"file z.txt"}; !slices.Equal(got, want) {
		t.Errorf("photos = %q, want %q", got, want)
	}
	if got := record(t, c, "/data", 1000, smaller); got.Missing != 0 {
		t.Errorf("second scan without them: Missing = %d, want 0 (already missing)", got.Missing)
	}

	// Back exactly as they were: still written, to clear the mark.
	got := record(t, c, "/data", 1000, sample)
	if want := map[uvsh.Kind]int{uvsh.Dir: 1, uvsh.File: 2}; !maps.Equal(got.Written, want) || got.Missing != 0 {
		t.Errorf("scan with them back: stats = %+v, want 2023/, img.jpg and zz.txt written", got)
	}
	stat(t, c, "/data/photos/2023/img.jpg")
}

// Not seeing an entry means nothing unless the whole tree was walked.

func testMissingNeedsCompleteScan(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, sample)
	stats, err := begin(t, c, "/data").Finish(t.Context(), false)
	must(t, err)
	if stats.Missing != 0 {
		t.Errorf("Missing = %d after an incomplete scan, want 0", stats.Missing)
	}
	stat(t, c, "/data/zz.txt")
}

// An entry that couldn't be read keeps what the catalog holds at and below it.

func testSkipKeepsWhatWasRecorded(t *testing.T, open Opener) {
	c := open(t)
	record(t, c, "/data", 1000, sample)
	ctx := t.Context()
	scanWith := func(add func(w catalog.ScanWriter)) catalog.ScanStats {
		t.Helper()
		w := begin(t, c, "/data")
		add(w)
		stats, err := w.Finish(ctx, true)
		must(t, err)
		return stats
	}

	// photos/ was listed but couldn't be read; zz.txt couldn't be stat'ed.
	stats := scanWith(func(w catalog.ScanWriter) {
		must(t, w.Add(ctx, uvsh.Entry{Path: "a.txt", Kind: uvsh.File, Size: 3, ModTime: t0}))
		must(t, w.Add(ctx, uvsh.Entry{Path: "photos", Kind: uvsh.Dir, ModTime: t0}))
		must(t, w.Skip(ctx, "photos"))
		must(t, w.Skip(ctx, "zz.txt"))
	})
	if stats.Missing != 0 {
		t.Errorf("Missing = %d, want 0", stats.Missing)
	}
	stat(t, c, "/data/photos/2023/img.jpg")
	stat(t, c, "/data/zz.txt")

	// The root itself couldn't be read: everything is kept.
	if stats := scanWith(func(w catalog.ScanWriter) { must(t, w.Skip(ctx, ".")) }); stats.Missing != 0 {
		t.Errorf("unreadable root: Missing = %d, want 0", stats.Missing)
	}
	stat(t, c, "/data/a.txt")
}

// Lookups inside one Read don't see a scan that commits while it runs.
