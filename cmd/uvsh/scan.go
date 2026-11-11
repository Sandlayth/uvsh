package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/sandlayth/uvsh/internal/scan"
)

func runScan(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "print entries instead of recording them")
	excludeFrom := fs.String("exclude-from", "", "read exclude patterns from `file`")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: uvsh scan -dry-run [-exclude-from file] dir")
		fmt.Fprintln(stderr, "\nWalk dir without following symlinks and list files, symlinks and other entries.")
		fmt.Fprintln(stderr, "\nflags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if !*dryRun || fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}

	var opts scan.Options
	if *excludeFrom != "" {
		f, err := os.Open(*excludeFrom)
		if err != nil {
			return err
		}
		opts.Exclude, err = scan.ReadExcludes(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", *excludeFrom, err)
		}
	}

	root, err := os.OpenRoot(fs.Arg(0))
	if err != nil {
		return err
	}
	defer root.Close()

	start := time.Now()
	counts := map[scan.Kind]int{}
	var failed int
	for e, err := range scan.Walk(ctx, root.FS(), opts) {
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			failed++
			logger.WarnContext(ctx, "skipped", "err", err)
			continue
		}
		counts[e.Kind]++
		printEntry(stdout, e)
	}

	logger.InfoContext(ctx, "scan complete",
		"files", counts[scan.File],
		"symlinks", counts[scan.Symlink],
		"other", counts[scan.Other],
		"errors", failed,
		"elapsed", time.Since(start).Round(time.Millisecond),
	)

	if failed > 0 {
		return fmt.Errorf("scan finished with %d errors", failed)
	}
	return nil
}

// printEntry writes one tab-separated line: kind, size, mtime, path[, -> target].
// Paths that aren't printable UTF-8 (newlines, invalid bytes) are Go-quoted so
// every entry stays on exactly one line.
func printEntry(w io.Writer, e scan.Entry) {
	switch e.Kind {
	case scan.File:
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", e.Kind, e.Size, e.ModTime.UTC().Format(time.RFC3339), quoteIfNeeded(e.Path))
	case scan.Symlink:
		fmt.Fprintf(w, "%s\t-\t-\t%s -> %s\n", e.Kind, quoteIfNeeded(e.Path), quoteIfNeeded(e.Target))
	default:
		fmt.Fprintf(w, "%s\t-\t-\t%s\n", e.Kind, quoteIfNeeded(e.Path))
	}
}

func quoteIfNeeded(s string) string {
	if q := strconv.Quote(s); q[1:len(q)-1] != s {
		return q
	}
	return s
}
