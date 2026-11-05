package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    error
		wantStdout string
		wantStderr string
	}{
		{name: "no command", args: []string{"uvsh"}, wantErr: errUsage, wantStderr: "usage:"},
		{name: "unknown command", args: []string{"uvsh", "frobnicate"}, wantErr: errUsage, wantStderr: `unknown command "frobnicate"`},
		{name: "bad flag", args: []string{"uvsh", "-nope"}, wantErr: errUsage, wantStderr: "flag provided but not defined"},
		{name: "help", args: []string{"uvsh", "-h"}, wantStderr: "usage:"},
		{name: "version", args: []string{"uvsh", "version"}, wantStdout: "platform:"},
		{name: "debug logs dispatch", args: []string{"uvsh", "-log-level", "debug", "version"}, wantStdout: "uvsh ", wantStderr: "command=version"},
		{name: "level is case-insensitive", args: []string{"uvsh", "-log-level=DEBUG", "version"}, wantStdout: "uvsh ", wantStderr: "command=version"},
		{name: "info hides debug", args: []string{"uvsh", "version"}, wantStdout: "uvsh "},
		{name: "invalid level", args: []string{"uvsh", "-log-level", "loud", "version"}, wantErr: errUsage, wantStderr: `invalid value "loud"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(t.Context(), tt.args, &stdout, &stderr)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("run() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantStdout == "" && stdout.Len() > 0 {
				t.Errorf("stdout should be empty, got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr == "" && stderr.Len() > 0 {
				t.Errorf("stderr should be empty, got %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
