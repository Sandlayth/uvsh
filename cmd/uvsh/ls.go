package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/backend"
	"github.com/sandlayth/uvsh/internal/index"
)

func runLs(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("db", "", "catalog `dsn`, as <backend>:<location> (required; backends: "+strings.Join(backend.Supported(), ", ")+")")
	page := fs.Int("page", 1000, "entries fetched per catalog query")
	after := fs.String("after", "", "start after the entries named `name`")
	limit := fs.Int("limit", 0, "stop after `n` entries (0: no limit)")
	recursive := fs.Bool("R", false, "list everything below path, depth first, as scan -dry-run prints it")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: uvsh ls -db dsn [-page n] [-after name] [-limit n] [path]")
		fmt.Fprintln(stderr, "       uvsh ls -db dsn -R [-page n] path")
		fmt.Fprintln(stderr, "\nList a directory from the catalog, without touching the disk. Without a path,")
		fmt.Fprintln(stderr, "list the registered roots.")
		fmt.Fprintln(stderr, "\nflags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	pageOpts := *after != "" || *limit != 0
	if *dsn == "" || fs.NArg() > 1 || *page < 1 || *limit < 0 || (fs.NArg() == 0 && (pageOpts || *recursive)) || (*recursive && pageOpts) {
		fs.Usage()
		return errUsage
	}

	cat, err := backend.Open(ctx, *dsn)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cat.Close()) }()

	if fs.NArg() == 0 {
		roots, err := cat.Roots(ctx)
		if err != nil {
			return err
		}
		for _, r := range roots {
			fmt.Fprintf(stdout, "root\t-\t%s\t%s\n", r.Added.UTC().Format(time.RFC3339), quoteIfNeeded(r.Path))
		}
		return nil
	}

	// No EvalSymlinks: the path is looked up in the catalog, never on disk.
	dir, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	if *recursive {
		for e, err := range index.Tree(ctx, cat, dir, *page) {
			if err != nil {
				return err
			}
			printEntry(stdout, e)
		}
		return nil
	}
	if *limit > 0 {
		*page = min(*page, *limit)
	}
	printed := 0
	for e, err := range index.List(ctx, cat, dir, *after, *page) {
		if errors.Is(err, catalog.ErrNotDir) && printed == 0 {
			if e, err = cat.Stat(ctx, dir); err != nil {
				return err
			}
			printEntry(stdout, e)
			return nil
		}
		if err != nil {
			return err
		}
		printEntry(stdout, e)
		if printed++; printed == *limit {
			break
		}
	}
	return nil
}
