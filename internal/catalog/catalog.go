// Package catalog is the contract between UVSH and its storage engines: the
// interface each engine implements and the rules all of them enforce (see the
// backend, sqlite and catalogtest subpackages).
package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/sandlayth/uvsh/internal/uvsh"
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
	// Stat returns the entry at path. Its Path is its base name.
	Stat(ctx context.Context, path string) (uvsh.Entry, error)
	// List returns up to limit entries of the dir at path whose names sort
	// after after, in byte order. Their Path is their name.
	List(ctx context.Context, path string, after string, limit int) ([]uvsh.Entry, error)
	Close() error
}

type Root struct {
	Path  string
	Added time.Time
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
	// Add records e, whose Path is relative to the scanned root. Other kinds
	// are rejected.
	Add(ctx context.Context, e uvsh.Entry) error
	// Finish commits the last batch and reports what was written. It must
	// still commit after the walk's ctx was cancelled (Ctrl-C): callers pass
	// a ctx that isn't, e.g. context.WithoutCancel(walkCtx).
	Finish(ctx context.Context) (ScanStats, error)
	// Abort discards the uncommitted batch. It is a no-op after Finish, so it
	// can always be deferred.
	Abort()
}

// ScanStats counts, per kind, entries inserted or changed (Written) and
// entries already up to date (Unchanged), plus the committed transactions.
type ScanStats struct {
	Written, Unchanged map[uvsh.Kind]int
	Batches            int
}
