package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path"
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
		return listTree(ctx, cat, dir, *page, stdout)
	}
	cursor := *after
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
			printEntry(stdout, e)
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			printEntry(stdout, e)
		}
		printed += len(entries)
		if len(entries) < n {
			break
		}
		cursor = entries[len(entries)-1].Path
	}
	return nil
}

// listTree prints dir and everything below it, in the order fs.WalkDir visits
// a disk: each dir right before its contents, names in byte order. Paths are
// relative to dir, starting with ".". The stack holds one frame per open dir,
// each with its current page, so memory is depth × page, not the tree's size.
func listTree(ctx context.Context, cat catalog.Catalog, dir string, page int, w io.Writer) error {
	top, err := cat.Stat(ctx, dir)
	if err != nil {
		return err
	}
	if top.Kind != catalog.Dir {
		printEntry(w, top)
		return nil
	}
	top.Path = "."
	printEntry(w, top)

	type frame struct {
		rel     string // slash-separated, relative to dir
		entries []catalog.Entry
		next    int  // index of the next entry to print
		more    bool // the last page was full, so another may follow
	}
	fetch := func(f *frame, after string) error {
		var err error
		f.entries, err = cat.List(ctx, filepath.Join(dir, filepath.FromSlash(f.rel)), after, page)
		f.next, f.more = 0, len(f.entries) == page
		return err
	}
	stack := []*frame{{rel: "."}}
	if err := fetch(stack[0], ""); err != nil {
		return err
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		if f.next == len(f.entries) {
			if !f.more {
				stack = stack[:len(stack)-1]
				continue
			}
			if err := fetch(f, f.entries[len(f.entries)-1].Path); err != nil {
				return err
			}
			continue
		}
		e := f.entries[f.next]
		f.next++
		rel := path.Join(f.rel, e.Path)
		e.Path = rel
		printEntry(w, e)
		if e.Kind == catalog.Dir {
			child := &frame{rel: rel}
			if err := fetch(child, ""); err != nil {
				return err
			}
			stack = append(stack, child)
		}
	}
	return nil
}
