package backend

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/catalogtest"
	"github.com/sandlayth/uvsh/internal/catalog/sqlite"
)

// Compile-time check that the engine satisfies the interface.
var _ catalog.Catalog = (*sqlite.Catalog)(nil)

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

func TestSupported(t *testing.T) {
	if got, want := Supported(), []string{"sqlite"}; !slices.Equal(got, want) {
		t.Errorf("Supported() = %v, want %v", got, want)
	}
}

func TestOpenSQLite(t *testing.T) {
	c, err := Open(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	info, err := c.Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.SchemaVersion < 1 {
		t.Errorf("SchemaVersion = %d, want >= 1", info.SchemaVersion)
	}
}

func TestOpenRejects(t *testing.T) {
	tests := []struct {
		name        string
		dsn         string
		unsupported bool
	}{
		{"unknown backend", "thallaja://localhost/uvsh", true},
		{"no backend", "/data/catalog.db", false},
		{"empty backend", ":/data/catalog.db", false},
		{"empty location", "sqlite:", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Open(t.Context(), tt.dsn)
			if err == nil {
				c.Close()
				t.Fatalf("Open(%q) succeeded", tt.dsn)
			}
			if got := errors.Is(err, ErrUnsupported); got != tt.unsupported {
				t.Errorf("errors.Is(err, ErrUnsupported) = %v, want %v (err: %v)", got, tt.unsupported, err)
			}
		})
	}
}

func TestOpenFailureReturnsNilCatalog(t *testing.T) {
	// A missing parent directory makes sqlite.Open fail.
	c, err := Open(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "missing", "catalog.db"))
	if err == nil {
		t.Fatal("Open succeeded")
	}
	if c != nil {
		t.Errorf("Open returned a non-nil Catalog (%T) alongside error %v", c, err)
	}
}
