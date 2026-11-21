package main

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
)

// printEntry writes one tab-separated line: kind, size, mtime, path[ -> target].
// scan -dry-run and ls -R both print through it, so their output compares.
// Paths that aren't printable UTF-8 (newlines, invalid bytes) are Go-quoted so
// every entry stays on exactly one line.
func printEntry(w io.Writer, e catalog.Entry) {
	size, name := "-", quoteIfNeeded(e.Path)
	switch e.Kind {
	case catalog.File:
		size = fmt.Sprint(e.Size)
	case catalog.Symlink:
		name += " -> " + quoteIfNeeded(e.Target)
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Kind, size, e.ModTime.UTC().Format(time.RFC3339), name)
}

func quoteIfNeeded(s string) string {
	if q := strconv.Quote(s); q[1:len(q)-1] != s {
		return q
	}
	return s
}
