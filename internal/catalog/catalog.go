// Package catalog defines the UVSH metadata model, independent of the
// storage engine that persists it (see the backend and sqlite subpackages).
package catalog

import (
	"context"
	"errors"
	"time"
)

var (
	ErrSchemaTooNew = errors.New("catalog schema is newer than this binary")
	ErrNotFound     = errors.New("not in the catalog")
	ErrNotDir       = errors.New("not a directory")
	ErrRootOverlap  = errors.New("overlaps the registered root")
)

// Catalog is what every storage engine provides.
type Catalog interface {
	Info(ctx context.Context) (Info, error)
	// BeginScan registers root (an absolute path) and returns a writer for one
	// walk of it.
	BeginScan(ctx context.Context, root string, opts ScanOptions) (ScanWriter, error)
	Roots(ctx context.Context) ([]Root, error)
	Stat(ctx context.Context, path string) (Entry, error)
	List(ctx context.Context, path string, after Cursor, limit int) ([]Entry, error)
	Close() error
}

type Root struct {
	Path  string
	Added time.Time
}

type Kind int

// The values order a dir before a file of the same name
const (
	Dir Kind = iota
	File
	Symlink
)

func (k Kind) String() string {
	switch k {
	case Dir:
		return "dir"
	case File:
		return "file"
	case Symlink:
		return "link"
	}
	return "unknown"
}

type Entry struct {
	Name    string
	Kind    Kind
	Size    int64 // files only
	ModTime time.Time
	Target  string // links only, as stored in the link
}

type Cursor struct {
	Name string
	Kind Kind
}

func (e Entry) Cursor() Cursor { return Cursor{e.Name, e.Kind} }

// Info describes the state of an opened catalog.
type Info struct {
	SchemaVersion int
}

type ScanOptions struct {
	// BatchSize is how many entries are written per transaction.
	BatchSize int
}

// ScanWriter records one walk. Entries must arrive parents first, as a
// depth-first walk yields them, starting with the root dir ".".
type ScanWriter interface {
	AddDir(ctx context.Context, d DirRecord) error
	AddFile(ctx context.Context, f FileRecord) error
	AddLink(ctx context.Context, l LinkRecord) error
	// Finish commits the last batch and reports what was written. It must
	// still commit after the walk's ctx was cancelled (Ctrl-C): callers pass
	// a ctx that isn't, e.g. context.WithoutCancel(walkCtx).
	Finish(ctx context.Context) (ScanStats, error)
	// Abort discards the uncommitted batch. It is a no-op after Finish, so it
	// can always be deferred.
	Abort()
}

// Paths are slash-separated and relative to the scanned root.
type DirRecord struct {
	Path    string
	ModTime time.Time
}

type FileRecord struct {
	Path    string
	Size    int64
	ModTime time.Time
}

type LinkRecord struct {
	Path    string
	Target  string // as stored in the link, never resolved
	ModTime time.Time
}

// ScanStats counts rows inserted or changed (Written) and rows already
// up to date (Unchanged), plus the number of committed transactions.
type ScanStats struct {
	DirsWritten, DirsUnchanged   int
	FilesWritten, FilesUnchanged int
	LinksWritten, LinksUnchanged int
	Batches                      int
}
