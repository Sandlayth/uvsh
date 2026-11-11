package main

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

func runVersion(stdout io.Writer) error {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("build info unavailable")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	fmt.Fprintf(stdout, "uvsh %s\n", info.Main.Version)
	fmt.Fprintf(stdout, "  commit:   %s (modified=%s)\n", settings["vcs.revision"], settings["vcs.modified"])
	fmt.Fprintf(stdout, "  go:       %s\n", info.GoVersion)
	fmt.Fprintf(stdout, "  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}
