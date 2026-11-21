package backend

import (
	"path/filepath"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/catalogtest"
)

// freshDSN gives each engine a new, empty catalog per test. An engine
// without an entry fails TestConformance, so none can skip the suite.
var freshDSN = map[string]func(t *testing.T) string{
	"sqlite": func(t *testing.T) string { return "sqlite:" + filepath.Join(t.TempDir(), "catalog.db") },
}

// Every registered engine must pass the catalog conformance suite.
func TestConformance(t *testing.T) {
	for _, name := range Supported() {
		t.Run(name, func(t *testing.T) {
			dsn, ok := freshDSN[name]
			if !ok {
				t.Fatalf("no test catalog for backend %q: add it to freshDSN", name)
			}
			catalogtest.Run(t, func(t *testing.T) catalog.Catalog {
				c, err := Open(t.Context(), dsn(t))
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				t.Cleanup(func() { c.Close() })
				return c
			})
		})
	}
}
