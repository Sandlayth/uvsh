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

// scannedTree records a small tree with fixed mtimes and returns its root and dsn.
func scannedTree(t *testing.T) (root, dsn string) {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	root = filepath.Join(tmp, "data")
	stamp := time.Date(2023, 1, 15, 12, 30, 0, 0, time.UTC)
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	for name, content := range map[string]string{"a.txt": "aaa", "new\nline.txt": "n", "sub/b.txt": "b", "z.txt": "zz"} {
		must(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0o644))
	}
	must(t, os.Symlink("a.txt", filepath.Join(root, "y")))
	for _, p := range []string{"a.txt", "new\nline.txt", "sub/b.txt", "z.txt", "sub", "."} {
		must(t, os.Chtimes(filepath.Join(root, p), stamp, stamp))
	}
	dsn = "sqlite:" + filepath.Join(t.TempDir(), "catalog.db")
	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"uvsh", "scan", "-db", dsn, root}, &stdout, &stderr); err != nil {
		t.Fatalf("scan: %v, stderr = %q", err, stderr.String())
	}
	return root, dsn
}

func ls(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), append([]string{"uvsh", "ls"}, args...), &stdout, &stderr)
	return stdout.String(), err
}

func TestLsUsage(t *testing.T) {
	runCLICases(t, []cliCase{
		{name: "requires -db", args: []string{"uvsh", "ls", "/data"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "rejects page below 1", args: []string{"uvsh", "ls", "-db", "sqlite:x.db", "-page", "0", "/data"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "rejects negative limit", args: []string{"uvsh", "ls", "-db", "sqlite:x.db", "-limit", "-1", "/data"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "rejects two paths", args: []string{"uvsh", "ls", "-db", "sqlite:x.db", "/a", "/b"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "rejects paging the roots", args: []string{"uvsh", "ls", "-db", "sqlite:x.db", "-limit", "1"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "rejects -R without a path", args: []string{"uvsh", "ls", "-db", "sqlite:x.db", "-R"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "rejects -R with -limit", args: []string{"uvsh", "ls", "-db", "sqlite:x.db", "-R", "-limit", "1", "/data"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "rejects -R with -after", args: []string{"uvsh", "ls", "-db", "sqlite:x.db", "-R", "-after", "a", "/data"}, wantErr: errUsage, wantStderr: "usage: uvsh ls"},
		{name: "help", args: []string{"uvsh", "ls", "-h"}, wantStderr: "usage: uvsh ls"},
	})
}

func TestLs(t *testing.T) {
	root, dsn := scannedTree(t)
	// os.Chtimes follows links, so the link keeps its creation time.
	y, err := os.Lstat(filepath.Join(root, "y"))
	must(t, err)
	link := "link\t-\t" + y.ModTime().UTC().Format(time.RFC3339) + "\ty -> a.txt\n"
	full := "file\t3\t2023-01-15T12:30:00Z\ta.txt\n" +
		"file\t1\t2023-01-15T12:30:00Z\t\"new\\nline.txt\"\n" +
		"dir\t-\t2023-01-15T12:30:00Z\tsub\n" + link +
		"file\t2\t2023-01-15T12:30:00Z\tz.txt\n"

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"dir", []string{root}, full},
		{"one entry per page", []string{"-page", "1", root}, full},
		{"limit", []string{"-limit", "2", "-page", "1", root}, "file\t3\t2023-01-15T12:30:00Z\ta.txt\n" +
			"file\t1\t2023-01-15T12:30:00Z\t\"new\\nline.txt\"\n"},
		{"after", []string{"-after", "sub", root}, link + "file\t2\t2023-01-15T12:30:00Z\tz.txt\n"},
		{"after a link", []string{"-after", "y", root}, "file\t2\t2023-01-15T12:30:00Z\tz.txt\n"},
		{"link", []string{root + "/y"}, link},
		{"subdir", []string{root + "/sub"}, "file\t1\t2023-01-15T12:30:00Z\tb.txt\n"},
		{"file", []string{root + "/a.txt"}, "file\t3\t2023-01-15T12:30:00Z\ta.txt\n"},
		{"recursive", []string{"-R", "-page", "1", root}, "dir\t-\t2023-01-15T12:30:00Z\t.\n" +
			"file\t3\t2023-01-15T12:30:00Z\ta.txt\n" +
			"file\t1\t2023-01-15T12:30:00Z\t\"new\\nline.txt\"\n" +
			"dir\t-\t2023-01-15T12:30:00Z\tsub\n" +
			"file\t1\t2023-01-15T12:30:00Z\tsub/b.txt\n" + link +
			"file\t2\t2023-01-15T12:30:00Z\tz.txt\n"},
		{"recursive on a file", []string{"-R", root + "/a.txt"}, "file\t3\t2023-01-15T12:30:00Z\ta.txt\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ls(t, append([]string{"-db", dsn}, tt.args...)...)
			if err != nil {
				t.Fatalf("ls: %v", err)
			}
			if got != tt.want {
				t.Errorf("stdout =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestLsRoots(t *testing.T) {
	root, dsn := scannedTree(t)
	got, err := ls(t, "-db", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "root\t-\t") || !strings.HasSuffix(got, "\t"+root+"\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("stdout = %q, want one root line for %s", got, root)
	}
}

func TestLsRelativePath(t *testing.T) {
	root, dsn := scannedTree(t)
	t.Chdir(root)
	got, err := ls(t, "-db", dsn, "sub")
	if err != nil || got != "file\t1\t2023-01-15T12:30:00Z\tb.txt\n" {
		t.Errorf("ls sub = %q, %v", got, err)
	}
}

func TestLsNotFound(t *testing.T) {
	root, dsn := scannedTree(t)
	for _, p := range []string{root + "/nope", "/elsewhere", root + "/a.txt/below"} {
		if _, err := ls(t, "-db", dsn, p); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("ls %s: error = %v, want ErrNotFound", p, err)
		}
	}
}
