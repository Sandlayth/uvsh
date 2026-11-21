package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
)

func TestScanUsage(t *testing.T) {
	runCLICases(t, []cliCase{
		{name: "requires -db or -dry-run", args: []string{"uvsh", "scan", "."}, wantErr: errUsage, wantStderr: "usage: uvsh scan"},
		{name: "rejects both -db and -dry-run", args: []string{"uvsh", "scan", "-dry-run", "-db", "sqlite:x.db", "."}, wantErr: errUsage, wantStderr: "usage: uvsh scan"},
		{name: "rejects batch below 1", args: []string{"uvsh", "scan", "-db", "sqlite:x.db", "-batch", "0", "."}, wantErr: errUsage, wantStderr: "usage: uvsh scan"},
		{name: "requires a dir", args: []string{"uvsh", "scan", "-dry-run"}, wantErr: errUsage, wantStderr: "usage: uvsh scan"},
		{name: "help", args: []string{"uvsh", "scan", "-h"}, wantStderr: "usage: uvsh scan"},
	})
}

func TestScanDryRun(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2023, 1, 15, 12, 30, 0, 0, time.UTC)
	for name, content := range map[string]string{"a.txt": "aaa", "new\nline.txt": "n", "skip.tmp": "s"} {
		p := filepath.Join(dir, name)
		must(t, os.WriteFile(p, []byte(content), 0o644))
		must(t, os.Chtimes(p, stamp, stamp))
	}
	must(t, os.Symlink("a.txt", filepath.Join(dir, "link")))
	must(t, os.Chtimes(dir, stamp, stamp))
	excludes := filepath.Join(t.TempDir(), "excludes")
	must(t, os.WriteFile(excludes, []byte("*.tmp\n"), 0o644))

	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"uvsh", "scan", "-dry-run", "-exclude-from", excludes, dir}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
	}

	// os.Chtimes follows links, so the link keeps its creation time.
	link, err := os.Lstat(filepath.Join(dir, "link"))
	must(t, err)
	want := "dir\t-\t2023-01-15T12:30:00Z\t.\n" +
		"file\t3\t2023-01-15T12:30:00Z\ta.txt\n" +
		"link\t-\t" + link.ModTime().UTC().Format(time.RFC3339) + "\tlink -> a.txt\n" +
		"file\t1\t2023-01-15T12:30:00Z\t\"new\\nline.txt\"\n"
	if stdout.String() != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout.String(), want)
	}
	if s := stderr.String(); !strings.Contains(s, "files=2 links=1 other=0 errors=0") {
		t.Errorf("stderr summary = %q", s)
	}
}

func TestScanMissingDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"uvsh", "scan", "-dry-run", filepath.Join(t.TempDir(), "nope")}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() succeeded on a missing dir")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanRecord(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o644))
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "catalog.db")

	scanOnce := func() string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := run(t.Context(), []string{"uvsh", "scan", "-db", dsn, "-batch", "2", dir}, &stdout, &stderr); err != nil {
			t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
		}
		if stdout.Len() > 0 {
			t.Errorf("recording should print nothing on stdout, got %q", stdout.String())
		}
		return stderr.String()
	}

	if s := scanOnce(); !strings.Contains(s, "dirs_written=2 dirs_unchanged=0 files_written=2 files_unchanged=0") {
		t.Errorf("first scan summary = %q", s)
	}
	if s := scanOnce(); !strings.Contains(s, "dirs_written=0 dirs_unchanged=2 files_written=0 files_unchanged=2") {
		t.Errorf("rescan summary = %q", s)
	}
}

func TestScanRootRealPath(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	real := filepath.Join(tmp, "real")
	must(t, os.MkdirAll(filepath.Join(real, "sub"), 0o755))
	must(t, os.Symlink(real, filepath.Join(tmp, "alias")))
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "catalog.db")

	scan := func(dir string) error {
		var stdout, stderr bytes.Buffer
		return run(t.Context(), []string{"uvsh", "scan", "-db", dsn, dir}, &stdout, &stderr)
	}
	must(t, scan(filepath.Join(tmp, "alias")))
	// Only overlaps if the alias was registered as its real path.
	if err := scan(filepath.Join(real, "sub")); !errors.Is(err, catalog.ErrRootOverlap) {
		t.Errorf("scan inside the root aliased by a symlink: error = %v, want ErrRootOverlap", err)
	}
}
