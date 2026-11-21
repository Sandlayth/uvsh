package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/backend"
	"github.com/sandlayth/uvsh/internal/index"
	"github.com/sandlayth/uvsh/internal/scan"
	"github.com/sandlayth/uvsh/internal/uvsh"
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

	opts := index.Options{
		BatchSize: *batch,
		OnSkip:    func(err error) { logger.WarnContext(ctx, "skipped", "err", err) },
	}
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

	start := time.Now()
	var rep index.Report
	if *dsn == "" {
		rep, err = index.Walk(ctx, fs.Arg(0), opts, func(e uvsh.Entry) error {
			printEntry(stdout, e, "")
			return nil
		})
	} else {
		var cat catalog.Catalog
		if cat, err = backend.Open(ctx, *dsn); err != nil {
			return err
		}
		defer func() { err = errors.Join(err, cat.Close()) }()
		rep, err = index.Record(ctx, cat, fs.Arg(0), opts)
	}
	interrupted := err != nil && ctx.Err() != nil
	if err != nil && !interrupted {
		return err
	}

	attrs := []any{
		"dirs", rep.Seen[uvsh.Dir],
		"files", rep.Seen[uvsh.File],
		"symlinks", rep.Seen[uvsh.Symlink],
		"other", rep.Seen[uvsh.Other],
		"errors", rep.Skipped,
	}
	if *dsn != "" {
		for _, k := range []uvsh.Kind{uvsh.Dir, uvsh.File, uvsh.Symlink} {
			attrs = append(attrs, string(k)+"s_written", rep.Stats.Written[k], string(k)+"s_unchanged", rep.Stats.Unchanged[k])
		}
		attrs = append(attrs, "missing", rep.Stats.Missing, "batches", rep.Stats.Batches)
	}
	attrs = append(attrs, "elapsed", time.Since(start).Round(time.Millisecond))
	if interrupted {
		logger.WarnContext(ctx, "scan interrupted", attrs...)
		return err
	}
	logger.InfoContext(ctx, "scan complete", attrs...)

	if rep.Skipped > 0 {
		return fmt.Errorf("scan finished with %d errors", rep.Skipped)
	}
	return nil
}
