package catalogtest

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
)

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
