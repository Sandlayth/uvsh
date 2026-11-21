package browse

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

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
