package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanUsage(t *testing.T) {
	runCLICases(t, []cliCase{
		{name: "requires -dry-run", args: []string{"uvsh", "scan", "."}, wantErr: errUsage, wantStderr: "usage: uvsh scan"},
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
	excludes := filepath.Join(t.TempDir(), "excludes")
	must(t, os.WriteFile(excludes, []byte("*.tmp\n"), 0o644))

	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"uvsh", "scan", "-dry-run", "-exclude-from", excludes, dir}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
	}

	want := "file\t3\t2023-01-15T12:30:00Z\ta.txt\n" +
		"link\t-\t-\tlink -> a.txt\n" +
		"file\t1\t2023-01-15T12:30:00Z\t\"new\\nline.txt\"\n"
	if stdout.String() != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout.String(), want)
	}
	if s := stderr.String(); !strings.Contains(s, "files=2 symlinks=1 other=0 errors=0") {
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
