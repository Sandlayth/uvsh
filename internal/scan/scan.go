// Package scan walks a directory tree and reports what it finds, without
// following symlinks or reading file contents.
package scan

import (
	"context"
	"fmt"
	"io/fs"
	"iter"
	"path"
	"strings"

	"github.com/sandlayth/uvsh/internal/catalog"
)

type Options struct {
	// A pattern without '/' matches an entry's name at any depth
	// A pattern with '/' matches its full relative path
	Exclude []string
}

func Walk(ctx context.Context, fsys fs.FS, opts Options) iter.Seq2[catalog.Entry, error] {
	return func(yield func(catalog.Entry, error) bool) {
		for _, pat := range opts.Exclude {
			if _, err := path.Match(pat, ""); err != nil {
				yield(catalog.Entry{}, fmt.Errorf("exclude pattern %q: %w", pat, err))
				return
			}
		}

		fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err := ctx.Err(); err != nil {
				yield(catalog.Entry{}, err)
				return fs.SkipAll
			}
			if err != nil {
				// WalkDir has already given up on this directory; carry on with the rest.
				if !yield(catalog.Entry{Path: p}, err) {
					return fs.SkipAll
				}
				return nil
			}
			if p != "." && opts.excluded(p, d.Name()) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			// Directories are yielded before their contents, the root (".") first.
			if !yield(entry(fsys, p, d)) {
				return fs.SkipAll
			}
			return nil
		})
	}
}

func entry(fsys fs.FS, p string, d fs.DirEntry) (catalog.Entry, error) {
	info, err := d.Info() // lstat: describes a symlink itself, not its target
	if err != nil {
		return catalog.Entry{Path: p}, err
	}
	e := catalog.Entry{Path: p, ModTime: info.ModTime()}
	switch mode := info.Mode(); {
	case mode.IsRegular():
		e.Kind, e.Size = catalog.File, info.Size()
	case mode.IsDir():
		e.Kind = catalog.Dir
	case mode&fs.ModeSymlink != 0:
		e.Kind = catalog.Symlink
		e.Target, err = fs.ReadLink(fsys, p)
	default:
		e.Kind = catalog.Other
	}
	return e, err
}

func (o Options) excluded(p, name string) bool {
	for _, pat := range o.Exclude {
		subject := name
		if strings.Contains(pat, "/") {
			subject = p
		}
		if ok, _ := path.Match(pat, subject); ok {
			return true
		}
	}
	return false
}
