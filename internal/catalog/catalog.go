// Package catalog defines the UVSH metadata model, independent of the
// storage engine that persists it (see the backend and sqlite subpackages).
package catalog

import (
	"context"
	"errors"
)

// ErrSchemaTooNew is returned when the catalog was written by a newer uvsh.
var ErrSchemaTooNew = errors.New("catalog schema is newer than this binary")

// Catalog is what every storage engine provides.
type Catalog interface {
	Info(ctx context.Context) (Info, error)
	Close() error
}

// Info describes the state of an opened catalog.
type Info struct {
	SchemaVersion int
}
