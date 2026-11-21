package index

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/sandlayth/uvsh/internal/browse"
	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/backend"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

// disk writes a small tree, with every kind of entry, and returns its real path.
func disk(t *testing.T) string {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	root := filepath.Join(tmp, "data")
	for _, d := range []string{"a/b/c", "a/z", "empty", "new\nline"} {
		must(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	for _, f := range []string{"a/b/c/deep.txt", "a/b/x.txt", "a/m.txt", "a/z/last.txt", "B.txt", "b.txt", "new\nline/f", "\xff.bin"} {
		must(t, os.WriteFile(filepath.Join(root, f), []byte(f), 0o644))
	}
	must(t, os.Symlink("../b.txt", filepath.Join(root, "a/to-b")))
	must(t, os.Symlink("nope", filepath.Join(root, "dangling")))
	must(t, syscall.Mkfifo(filepath.Join(root, "fifo"), 0o644))
	return root
}

func open(t *testing.T) catalog.Catalog {
	t.Helper()
	c, err := backend.Open(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "catalog.db"))
	must(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, seq func(func(uvsh.Entry, error) bool)) []uvsh.Entry {
	t.Helper()
	var got []uvsh.Entry
	for e, err := range seq {
		must(t, err)
		got = append(got, e)
	}
	return got
}

func TestRecord(t *testing.T) {
	root, c := disk(t), open(t)
	rep, err := Record(t.Context(), c, root, Options{BatchSize: 4})
	must(t, err)
	seen := map[uvsh.Kind]int{uvsh.Dir: 7, uvsh.File: 8, uvsh.Link: 2, uvsh.Other: 1}
	if !maps.Equal(rep.Seen, seen) || rep.Skipped != 0 {
		t.Errorf("seen = %v, skipped = %d; want %v, 0", rep.Seen, rep.Skipped, seen)
	}
	delete(seen, uvsh.Other) // walked, never stored
	if !maps.Equal(rep.Stats.Written, seen) {
		t.Errorf("written = %v, want %v", rep.Stats.Written, seen)
	}
	if _, err := c.Stat(t.Context(), filepath.Join(root, "fifo")); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Stat(fifo) error = %v, want ErrNotFound", err)
	}

	rep, err = Record(t.Context(), c, root, Options{BatchSize: 4})
	must(t, err)
	if len(rep.Stats.Written) != 0 || !maps.Equal(rep.Stats.Unchanged, seen) {
		t.Errorf("rescan stats = %+v, want everything unchanged", rep.Stats)
	}
}

func TestRecordReportsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root, c := disk(t), open(t)
	locked := filepath.Join(root, "a/z")
	must(t, os.Chmod(locked, 0))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	var skipped []error
	rep, err := Record(t.Context(), c, root, Options{BatchSize: 1000, OnSkip: func(err error) { skipped = append(skipped, err) }})
	must(t, err)
	if rep.Skipped != 1 || len(skipped) != 1 {
		t.Errorf("skipped = %d, OnSkip got %v; want 1 each", rep.Skipped, skipped)
	}
	if _, err := c.Stat(t.Context(), filepath.Join(root, "a/m.txt")); err != nil {
		t.Errorf("the rest of the walk wasn't recorded: %v", err)
	}
}

func TestRecordMarksMissing(t *testing.T) {
	root, c := disk(t), open(t)
	_, err := Record(t.Context(), c, root, Options{BatchSize: 1000})
	must(t, err)
	must(t, os.RemoveAll(filepath.Join(root, "a/z")))
	must(t, os.Remove(filepath.Join(root, "b.txt")))
	rep, err := Record(t.Context(), c, root, Options{BatchSize: 1000})
	must(t, err)
	if rep.Stats.Missing != 3 {
		t.Errorf("Missing = %d, want 3 (a/z, a/z/last.txt, b.txt)", rep.Stats.Missing)
	}
	for _, p := range []string{"a/z/last.txt", "b.txt"} {
		if _, err := c.Stat(t.Context(), filepath.Join(root, p)); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("Stat(%s) error = %v, want ErrNotFound", p, err)
		}
	}
}

// A walk that couldn't even start must not be taken for a complete one that
// found nothing.

func TestRecordBadExcludeMarksNothing(t *testing.T) {
	root, c := disk(t), open(t)
	_, err := Record(t.Context(), c, root, Options{BatchSize: 1000})
	must(t, err)
	if _, err := Record(t.Context(), c, root, Options{BatchSize: 1000, Exclude: []string{"["}}); err == nil {
		t.Fatal("Record with a bad exclude pattern succeeded")
	}
	if _, err := c.Stat(t.Context(), filepath.Join(root, "b.txt")); err != nil {
		t.Errorf("b.txt after the failed scan: %v", err)
	}
	if _, err := Walk(t.Context(), root, Options{Exclude: []string{"["}}, func(uvsh.Entry) error { return nil }); err == nil {
		t.Error("Walk with a bad exclude pattern succeeded")
	}
}

func TestRecordKeepsUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root, c := disk(t), open(t)
	_, err := Record(t.Context(), c, root, Options{BatchSize: 1000})
	must(t, err)
	locked := filepath.Join(root, "a/z")
	must(t, os.Chmod(locked, 0))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	rep, err := Record(t.Context(), c, root, Options{BatchSize: 1000})
	must(t, err)
	if rep.Skipped != 1 || rep.Stats.Missing != 0 {
		t.Errorf("skipped = %d, missing = %d; want 1, 0", rep.Skipped, rep.Stats.Missing)
	}
	if _, err := c.Stat(t.Context(), filepath.Join(locked, "last.txt")); err != nil {
		t.Errorf("last.txt in the unreadable dir: %v", err)
	}
}

// cancelling cancels its ctx once n entries were added, as Ctrl-C would.

type cancelling struct {
	catalog.Catalog
	n      int
	cancel context.CancelFunc
}

func (c *cancelling) BeginScan(ctx context.Context, root string, opts catalog.ScanOptions) (catalog.ScanWriter, error) {
	w, err := c.Catalog.BeginScan(ctx, root, opts)
	return &cancellingWriter{ScanWriter: w, c: c}, err
}

type cancellingWriter struct {
	catalog.ScanWriter
	c *cancelling
}

func (w *cancellingWriter) Add(ctx context.Context, e uvsh.Entry) error {
	if w.c.n--; w.c.n == 0 {
		w.c.cancel()
	}
	return w.ScanWriter.Add(ctx, e)
}

func TestRecordCommitsWhatWasWalkedWhenInterrupted(t *testing.T) {
	root, c := disk(t), open(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := Record(ctx, &cancelling{Catalog: c, n: 3, cancel: cancel}, root, Options{BatchSize: 1000})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	// The third Add runs on the cancelled ctx and fails; the two before it sit
	// in a batch of 1000 that never filled, so only Finish can commit them.
	if got := collect(t, browse.Tree(t.Context(), c, root, 100)); len(got) != 2 {
		t.Errorf("catalog holds %d entries after the interrupt, want the 2 added before it", len(got))
	}
}

// What Tree reads back from the catalog must be exactly what Walk saw on
// disk, in the same order, whatever the page size.
