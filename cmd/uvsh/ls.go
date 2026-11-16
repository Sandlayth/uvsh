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
)

func runLs(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("db", "", "catalog `dsn`, as <backend>:<location> (required; backends: "+strings.Join(backend.Supported(), ", ")+")")
	page := fs.Int("page", 1000, "entries fetched per catalog query")
	after := fs.String("after", "", "start after the entries named `name`")
	limit := fs.Int("limit", 0, "stop after `n` entries (0: no limit)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: uvsh ls -db dsn [-page n] [-after name] [-limit n] [path]")
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
	if *dsn == "" || fs.NArg() > 1 || *page < 1 || *limit < 0 || (fs.NArg() == 0 && pageOpts) {
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
	// File sorts after Dir, so this cursor skips both entries named -after.
	cursor := catalog.Cursor{Name: *after, Kind: catalog.File}
	for printed := 0; *limit == 0 || printed < *limit; {
		n := *page
		if *limit > 0 {
			n = min(n, *limit-printed)
		}
		entries, err := cat.List(ctx, dir, cursor, n)
		if errors.Is(err, catalog.ErrNotDir) && printed == 0 {
			e, err := cat.Stat(ctx, dir)
			if err != nil {
				return err
			}
			printCatalogEntry(stdout, e)
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			printCatalogEntry(stdout, e)
		}
		printed += len(entries)
		if len(entries) < n {
			break
		}
		cursor = entries[len(entries)-1].Cursor()
	}
	return nil
}

// printCatalogEntry uses the columns of scan -dry-run: kind, size, mtime, name.
func printCatalogEntry(w io.Writer, e catalog.Entry) {
	size := "-"
	if e.Kind == catalog.File {
		size = fmt.Sprint(e.Size)
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Kind, size, e.ModTime.UTC().Format(time.RFC3339), quoteIfNeeded(e.Name))
}
