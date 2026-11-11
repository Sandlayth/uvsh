// Package catalog defines the UVSH metadata model, independent of the
// storage engine that persists it (see the backend and sqlite subpackages).
package catalog

import (
	"context"
	"errors"
	"time"
)

// ErrSchemaTooNew is returned when the catalog was written by a newer uvsh.
var ErrSchemaTooNew = errors.New("catalog schema is newer than this binary")

// Catalog is what every storage engine provides.
type Catalog interface {
	Info(ctx context.Context) (Info, error)
	// BeginScan registers root (an absolute path) and returns a writer for one
	// walk of it.
	BeginScan(ctx context.Context, root string, opts ScanOptions) (ScanWriter, error)
	Close() error
}

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

// ScanStats counts rows inserted or changed (Written) and rows already
// up to date (Unchanged), plus the number of committed transactions.
type ScanStats struct {
	DirsWritten, DirsUnchanged   int
	FilesWritten, FilesUnchanged int
	Batches                      int
}
