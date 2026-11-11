package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var stamp = time.Date(2023, 1, 15, 12, 30, 0, 0, time.UTC)

// fixture builds:
//
//	outside/secret.txt
//	tree/a.txt                  (3 bytes)
//	tree/dir/b.txt              (5 bytes)
//	tree/dir/.cache/c.txt
//	tree/#trash/d.txt
//	tree/x.tmp
//	tree/links/{to-file,to-dir,to-outside,dangling,loop-a,loop-b}
//	tree/fifo
//
// and returns the tree's path.
func fixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	tree := filepath.Join(base, "tree")
	files := map[string]string{
		"outside/secret.txt":    "secret",
		"tree/a.txt":            "aaa",
		"tree/dir/b.txt":        "bbbbb",
		"tree/dir/.cache/c.txt": "c",
		"tree/#trash/d.txt":     "d",
		"tree/x.tmp":            "x",
	}
	for name, content := range files {
		p := filepath.Join(base, name)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(content), 0o644))
		must(t, os.Chtimes(p, stamp, stamp))
	}
	links := map[string]string{
		"to-file":    "../a.txt",
		"to-dir":     "../dir",
		"to-outside": "../../outside/secret.txt",
		"dangling":   "../nope.txt",
		"loop-a":     "loop-b",
		"loop-b":     "loop-a",
	}
	must(t, os.Mkdir(filepath.Join(tree, "links"), 0o755))
	for name, target := range links {
		must(t, os.Symlink(target, filepath.Join(tree, "links", name)))
	}
	must(t, syscall.Mkfifo(filepath.Join(tree, "fifo"), 0o644))
	return tree
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// walkAll opens dir as an os.Root, like the CLI does, and collects everything.
func walkAll(t *testing.T, dir string, opts Options) (map[string]Entry, []error) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	must(t, err)
	t.Cleanup(func() { root.Close() })

	entries := map[string]Entry{}
	var errs []error
	for e, err := range Walk(t.Context(), root.FS(), opts) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		entries[e.Path] = e
	}
	return entries, errs
}

func TestWalkReportsEveryKind(t *testing.T) {
	entries, errs := walkAll(t, fixture(t), Options{})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	want := map[string]Entry{
		".":                {Kind: Dir},
		"dir":              {Kind: Dir},
		"dir/.cache":       {Kind: Dir},
		"#trash":           {Kind: Dir},
		"links":            {Kind: Dir},
		"a.txt":            {Kind: File, Size: 3, ModTime: stamp},
		"dir/b.txt":        {Kind: File, Size: 5, ModTime: stamp},
		"dir/.cache/c.txt": {Kind: File, Size: 1, ModTime: stamp},
		"#trash/d.txt":     {Kind: File, Size: 1, ModTime: stamp},
		"x.tmp":            {Kind: File, Size: 1, ModTime: stamp},
		"links/to-file":    {Kind: Symlink, Target: "../a.txt"},
		"links/to-dir":     {Kind: Symlink, Target: "../dir"},
		"links/to-outside": {Kind: Symlink, Target: "../../outside/secret.txt"},
		"links/dangling":   {Kind: Symlink, Target: "../nope.txt"},
		"links/loop-a":     {Kind: Symlink, Target: "loop-b"},
		"links/loop-b":     {Kind: Symlink, Target: "loop-a"},
		"fifo":             {Kind: Other},
	}
	if len(entries) != len(want) {
		t.Errorf("got %d entries, want %d: %v", len(entries), len(want), keys(entries))
	}
	for p, w := range want {
		got, ok := entries[p]
		if !ok {
			t.Errorf("missing %s", p)
			continue
		}
		if got.Kind != w.Kind || got.Target != w.Target {
			t.Errorf("%s: kind=%v target=%q, want kind=%v target=%q", p, got.Kind, got.Target, w.Kind, w.Target)
		}
		if w.Kind == File && (got.Size != w.Size || !got.ModTime.Equal(w.ModTime)) {
			t.Errorf("%s: size=%d mtime=%v, want size=%d mtime=%v", p, got.Size, got.ModTime, w.Size, w.ModTime)
		}
	}
	// Following links/to-dir would have produced links/to-dir/b.txt.
	for p := range entries {
		if strings.HasPrefix(p, "links/to-dir/") {
			t.Errorf("walked through a symlinked directory: %s", p)
		}
	}
}

func TestWalkExcludes(t *testing.T) {
	entries, errs := walkAll(t, fixture(t), Options{Exclude: []string{".cache", "*.tmp", `\#trash`, "links/loop-*"}})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, p := range []string{"dir/.cache", "dir/.cache/c.txt", "x.tmp", "#trash", "#trash/d.txt", "links/loop-a", "links/loop-b"} {
		if _, ok := entries[p]; ok {
			t.Errorf("%s should be excluded", p)
		}
	}
	for _, p := range []string{"a.txt", "dir/b.txt", "links/to-file"} {
		if _, ok := entries[p]; !ok {
			t.Errorf("%s should not be excluded", p)
		}
	}
}

func TestWalkBadExcludePattern(t *testing.T) {
	_, errs := walkAll(t, fixture(t), Options{Exclude: []string{"[unclosed"}})
	if len(errs) != 1 || !errors.Is(errs[0], path.ErrBadPattern) {
		t.Fatalf("errs = %v, want one ErrBadPattern", errs)
	}
}

func TestWalkContinuesPastUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	tree := fixture(t)
	locked := filepath.Join(tree, "dir")
	must(t, os.Chmod(locked, 0))
	t.Cleanup(func() { os.Chmod(locked, 0o755) }) // let TempDir cleanup remove it

	entries, errs := walkAll(t, tree, Options{})
	if len(errs) != 1 || !errors.Is(errs[0], fs.ErrPermission) {
		t.Fatalf("errs = %v, want one permission error", errs)
	}
	if _, ok := entries["links/to-file"]; !ok {
		t.Error("walk stopped at the unreadable dir instead of continuing")
	}
}

func TestWalkStopsOnBreak(t *testing.T) {
	root, err := os.OpenRoot(fixture(t))
	must(t, err)
	defer root.Close()

	// The runtime panics if an iterator keeps calling yield after a break,
	// so reaching the end of this loop is the assertion.
	n := 0
	for range Walk(t.Context(), root.FS(), Options{}) {
		n++
		break
	}
	if n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}
}

func TestWalkStopsOnCancel(t *testing.T) {
	root, err := os.OpenRoot(fixture(t))
	must(t, err)
	defer root.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var got []error
	for _, err := range Walk(ctx, root.FS(), Options{}) {
		got = append(got, err)
	}
	if len(got) != 1 || !errors.Is(got[0], context.Canceled) {
		t.Fatalf("yielded %v, want exactly one context.Canceled", got)
	}
}

func keys(m map[string]Entry) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
