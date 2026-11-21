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

	handle := func(e catalog.Entry) error {
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
		handle = func(e catalog.Entry) error {
			if e.Kind == catalog.Other {
				return nil // walked and counted, never stored
			}
			return w.Add(ctx, e)
		}
	}

	start := time.Now()
	counts := map[catalog.Kind]int{}
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
		"dirs", counts[catalog.Dir],
		"files", counts[catalog.File],
		"symlinks", counts[catalog.Symlink],
		"other", counts[catalog.Other],
		"errors", failed,
	}
	if w != nil {
		// Commit what was walked even after Ctrl-C: those rows are true. ctx is
		// cancelled by then, so the commit runs on a context that isn't.
		stats, err := w.Finish(context.WithoutCancel(ctx))
		if err != nil {
			return err
		}
		for _, k := range []catalog.Kind{catalog.Dir, catalog.File, catalog.Symlink} {
			attrs = append(attrs, string(k)+"s_written", stats.Written[k], string(k)+"s_unchanged", stats.Unchanged[k])
		}
		attrs = append(attrs, "batches", stats.Batches)
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
