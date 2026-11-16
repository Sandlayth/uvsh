package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/backend"
	"github.com/sandlayth/uvsh/internal/scan"
)

func runScan(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) (err error) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("db", "", "record into the catalog `dsn`, as <backend>:<location> (backends: "+strings.Join(backend.Supported(), ", ")+")")
	dryRun := fs.Bool("dry-run", false, "print entries instead of recording them")
	batch := fs.Int("batch", 1000, "entries written per catalog transaction")
	excludeFrom := fs.String("exclude-from", "", "read exclude patterns from `file`")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: uvsh scan (-db dsn | -dry-run) [-batch n] [-exclude-from file] dir")
		fmt.Fprintln(stderr, "\nWalk dir without following symlinks and record its directories and files in")
		fmt.Fprintln(stderr, "the catalog, or with -dry-run list every entry instead.")
		fmt.Fprintln(stderr, "\nflags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if *dryRun == (*dsn != "") || fs.NArg() != 1 || *batch < 1 {
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

	dir, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	handle := func(e scan.Entry) error {
		printEntry(stdout, e)
		return nil
	}
	var w catalog.ScanWriter
	if *dsn != "" {
		cat, err := backend.Open(ctx, *dsn)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, cat.Close()) }()
		if w, err = cat.BeginScan(ctx, dir, catalog.ScanOptions{BatchSize: *batch}); err != nil {
			return err
		}
		defer w.Abort()
		handle = func(e scan.Entry) error { return record(ctx, w, e) }
	}

	start := time.Now()
	counts := map[scan.Kind]int{}
	var failed int
	var interrupted error
	for e, err := range scan.Walk(ctx, root.FS(), opts) {
		if err != nil {
			if ctx.Err() != nil {
				interrupted = err
				break
			}
			failed++
			logger.WarnContext(ctx, "skipped", "err", err)
			continue
		}
		counts[e.Kind]++
		if err := handle(e); err != nil {
			return err
		}
	}

	attrs := []any{
		"dirs", counts[scan.Dir],
		"files", counts[scan.File],
		"symlinks", counts[scan.Symlink],
		"other", counts[scan.Other],
		"errors", failed,
	}
	if w != nil {
		// Commit what was walked even after Ctrl-C: those rows are true. ctx is
		// cancelled by then, so the commit runs on a context that isn't.
		stats, err := w.Finish(context.WithoutCancel(ctx))
		if err != nil {
			return err
		}
		attrs = append(attrs,
			"dirs_written", stats.DirsWritten,
			"files_written", stats.FilesWritten,
			"files_unchanged", stats.FilesUnchanged,
			"batches", stats.Batches,
		)
	}
	attrs = append(attrs, "elapsed", time.Since(start).Round(time.Millisecond))
	if interrupted != nil {
		logger.WarnContext(ctx, "scan interrupted", attrs...)
		return interrupted
	}
	logger.InfoContext(ctx, "scan complete", attrs...)

	if failed > 0 {
		return fmt.Errorf("scan finished with %d errors", failed)
	}
	return nil
}

func record(ctx context.Context, w catalog.ScanWriter, e scan.Entry) error {
	switch e.Kind {
	case scan.Dir:
		return w.AddDir(ctx, catalog.DirRecord{Path: e.Path, ModTime: e.ModTime})
	case scan.File:
		return w.AddFile(ctx, catalog.FileRecord{Path: e.Path, Size: e.Size, ModTime: e.ModTime})
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
