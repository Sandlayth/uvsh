// Package browse reads what a catalog holds: listings, whole trees, and what
// links resolve to. It only uses the catalog.Catalog interface.
package browse

import (
	"context"
	"iter"
	"path"
	"path/filepath"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

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
