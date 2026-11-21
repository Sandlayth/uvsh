// Package index records directories into a catalog and walks what a catalog
// holds. It only uses the catalog.Catalog interface, so it works on any engine.
package index

import (
	"context"
	"errors"
	"iter"
	"os"
	"path"
	"path/filepath"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/scan"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

type Options struct {
	Exclude   []string // see scan.Options
	BatchSize int      // Record only: entries written per transaction
	// OnSkip is told about each entry that couldn't be read; the walk goes on.
	OnSkip func(err error)
}

type Report struct {
	Root    string            // the real path walked
	Seen    map[uvsh.Kind]int // everything walked, Other included
	Skipped int
	Stats   catalog.ScanStats // Record only
}

// Walk walks the real path of dir and calls fn with each entry, parents
// first. It stops at fn's first error, or with ctx's error once cancelled.
func Walk(ctx context.Context, dir string, opts Options, fn func(uvsh.Entry) error) (Report, error) {
	root, err := realPath(dir)
	if err != nil {
		return Report{}, err
	}
	return walk(ctx, root, opts, fn, nil)
}

// Record walks dir into cat. Entries recorded before but not found this time
// are marked missing, once the whole tree was walked. Once ctx is cancelled
// (Ctrl-C) it still commits what was walked, since those rows are true, and
// returns ctx's error.
func Record(ctx context.Context, cat catalog.Catalog, dir string, opts Options) (Report, error) {
	// Register the real path, so a symlinked alias can't slip past the root
	// overlap check. Only the root is resolved; the walk never follows links.
	root, err := realPath(dir)
	if err != nil {
		return Report{}, err
	}
	w, err := cat.BeginScan(ctx, root, catalog.ScanOptions{BatchSize: opts.BatchSize})
	if err != nil {
		return Report{}, err
	}
	defer w.Abort()
	add := func(e uvsh.Entry) error {
		if e.Kind == uvsh.Other {
			return nil // walked and counted, never stored
		}
		return w.Add(ctx, e)
	}
	skip := func(p string) error { return w.Skip(ctx, p) }
	rep, walkErr := walk(ctx, root, opts, add, skip)
	if walkErr != nil && ctx.Err() == nil {
		return rep, walkErr
	}
	rep.Stats, err = w.Finish(context.WithoutCancel(ctx), walkErr == nil)
	return rep, errors.Join(walkErr, err)
}

// walk calls fn with each entry and skip, if not nil, with the path of each
// entry that couldn't be read.
func walk(ctx context.Context, root string, opts Options, fn func(uvsh.Entry) error, skip func(path string) error) (Report, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return Report{}, err
	}
	defer r.Close()
	rep := Report{Root: root, Seen: map[uvsh.Kind]int{}}
	for e, err := range scan.Walk(ctx, r.FS(), scan.Options{Exclude: opts.Exclude}) {
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return rep, err
		case e.Path == "":
			// Not about an entry (a bad exclude pattern): the walk is over,
			// and a walk that stopped early must not pass for complete.
			return rep, err
		default:
			rep.Skipped++
			if opts.OnSkip != nil {
				opts.OnSkip(err)
			}
			if skip != nil {
				if err := skip(e.Path); err != nil {
					return rep, err
				}
			}
			continue
		}
		rep.Seen[e.Kind]++
		if err := fn(e); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func realPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// List yields the entries of the dir at p whose names sort after after,
// fetching page entries per query. Each page is read in its own short
// transaction, so a long listing doesn't pin an old snapshot.
func List(ctx context.Context, cat catalog.Catalog, p, after string, page int) iter.Seq2[uvsh.Entry, error] {
	return func(yield func(uvsh.Entry, error) bool) {
		for {
			entries, err := cat.List(ctx, p, after, page)
			if err != nil {
				yield(uvsh.Entry{}, err)
				return
			}
			for _, e := range entries {
				if !yield(e, nil) {
					return
				}
			}
			if len(entries) < page {
				return
			}
			after = entries[len(entries)-1].Path
		}
	}
}

// Tree yields the entry at p and, for a dir, everything below it, in the
// order scan.Walk visits a disk: each dir right before its contents, names in
// byte order. Paths are relative to p, which is ".". Each open dir holds one
// page, so memory follows the tree's depth, not its size.
func Tree(ctx context.Context, cat catalog.Catalog, p string, page int) iter.Seq2[uvsh.Entry, error] {
	return func(yield func(uvsh.Entry, error) bool) {
		top, err := cat.Stat(ctx, p)
		if err != nil {
			yield(uvsh.Entry{}, err)
			return
		}
		if top.Kind != uvsh.Dir {
			yield(top, nil)
			return
		}
		top.Path = "."
		if yield(top, nil) {
			below(ctx, cat, p, ".", page, yield)
		}
	}
}

// below yields what is under dir, with paths under rel. It returns false
// once the walk must stop: yield returned false, or after yielding an error.
func below(ctx context.Context, cat catalog.Catalog, dir, rel string, page int, yield func(uvsh.Entry, error) bool) bool {
	for e, err := range List(ctx, cat, dir, "", page) {
		if err != nil {
			yield(uvsh.Entry{}, err)
			return false
		}
		name := e.Path
		e.Path = path.Join(rel, name)
		if !yield(e, nil) {
			return false
		}
		if e.Kind == uvsh.Dir && !below(ctx, cat, filepath.Join(dir, name), e.Path, page, yield) {
			return false
		}
	}
	return true
}
