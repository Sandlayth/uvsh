// Package backend opens a catalog on the storage engine named in its DSN.
// It is the only package that knows every engine.
package backend

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/sqlite"
)

// ErrUnsupported is returned for a DSN naming an engine this binary lacks.
var ErrUnsupported = errors.New("unsupported catalog backend")

type opener func(ctx context.Context, location string) (catalog.Catalog, error)

var openers = map[string]opener{
	"sqlite": func(ctx context.Context, location string) (catalog.Catalog, error) {
		c, err := sqlite.Open(ctx, location)
		if err != nil {
			// Returning c here would wrap a nil *sqlite.Catalog in a non-nil interface.
			return nil, err
		}
		return c, nil
	},
}

// Open opens the catalog described by dsn, written "<backend>:<location>",
// e.g. "sqlite:/data/uvsh/catalog.db".
func Open(ctx context.Context, dsn string) (catalog.Catalog, error) {
	name, location, ok := strings.Cut(dsn, ":")
	if !ok || name == "" || location == "" {
		return nil, fmt.Errorf("catalog dsn %q: want <backend>:<location> with backend one of %s", dsn, supportedList())
	}
	open, ok := openers[name]
	if !ok {
		return nil, fmt.Errorf("%w %q (supported: %s)", ErrUnsupported, name, supportedList())
	}
	return open(ctx, location)
}

// Supported lists the backend names this binary can open, sorted.
func Supported() []string {
	return slices.Sorted(maps.Keys(openers))
}

func supportedList() string {
	return strings.Join(Supported(), ", ")
}
