package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitUsage(t *testing.T) {
	runCLICases(t, []cliCase{
		{name: "requires -db", args: []string{"uvsh", "init"}, wantErr: errUsage, wantStderr: "usage: uvsh init"},
		{name: "rejects extra args", args: []string{"uvsh", "init", "-db", "x.db", "extra"}, wantErr: errUsage, wantStderr: "usage: uvsh init"},
		{name: "help", args: []string{"uvsh", "init", "-h"}, wantStderr: "usage: uvsh init"},
		{name: "help lists backends", args: []string{"uvsh", "init", "-h"}, wantStderr: "backends: sqlite"},
	})
}

func TestInit(t *testing.T) {
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "catalog.db")

	// Twice: the first run creates the catalog, the second must find it up to date.
	for range 2 {
		var stdout, stderr bytes.Buffer
		if err := run(t.Context(), []string{"uvsh", "init", "-db", dsn}, &stdout, &stderr); err != nil {
			t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
		}
		if want := "schema v1"; !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestInitMissingDirectory(t *testing.T) {
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "missing", "catalog.db")
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"uvsh", "init", "-db", dsn}, &stdout, &stderr)
	if err == nil || errors.Is(err, errUsage) {
		t.Fatalf("run() error = %v, want a non-usage error", err)
	}
}

func TestInitUnsupportedBackend(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"uvsh", "init", "-db", "thallaja://localhost/uvsh"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "supported: sqlite") {
		t.Fatalf("run() error = %v, want an unsupported-backend error listing sqlite", err)
	}
}
