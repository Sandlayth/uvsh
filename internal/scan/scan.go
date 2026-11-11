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
	"time"
)

type Kind uint8

const (
	File Kind = iota + 1 // regular file
	Dir
	Symlink
	Other
)

func (k Kind) String() string {
	switch k {
	case File:
		return "file"
	case Dir:
		return "dir"
	case Symlink:
		return "link"
	case Other:
		return "other"
	}
	return fmt.Sprintf("Kind(%d)", k)
}

type Entry struct {
	Path    string // slash-separated, relative to the walked root
	Kind    Kind
	Size    int64
	ModTime time.Time
	Target  string // symlink destination as stored in the link; empty otherwise
}

type Options struct {
	// A pattern without '/' matches an entry's name at any depth
	// A pattern with '/' matches its full relative path
	Exclude []string
}

func Walk(ctx context.Context, fsys fs.FS, opts Options) iter.Seq2[Entry, error] {
	return func(yield func(Entry, error) bool) {
		for _, pat := range opts.Exclude {
			if _, err := path.Match(pat, ""); err != nil {
				yield(Entry{}, fmt.Errorf("exclude pattern %q: %w", pat, err))
				return
			}
		}

		fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err := ctx.Err(); err != nil {
				yield(Entry{}, err)
				return fs.SkipAll
			}
			if err != nil {
				// WalkDir has already given up on this directory; carry on with the rest.
				if !yield(Entry{Path: p}, err) {
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

func entry(fsys fs.FS, p string, d fs.DirEntry) (Entry, error) {
	info, err := d.Info() // lstat: describes a symlink itself, not its target
	if err != nil {
		return Entry{Path: p}, err
	}
	e := Entry{Path: p, Size: info.Size(), ModTime: info.ModTime()}
	switch mode := info.Mode(); {
	case mode.IsRegular():
		e.Kind = File
	case mode.IsDir():
		e.Kind = Dir
		e.Size = 0
	case mode&fs.ModeSymlink != 0:
		e.Kind = Symlink
		e.Target, err = fs.ReadLink(fsys, p)
	default:
		e.Kind = Other
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
