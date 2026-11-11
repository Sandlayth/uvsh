package backend

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/catalog/sqlite"
)

// Compile-time check that the engine satisfies the interface.
var _ catalog.Catalog = (*sqlite.Catalog)(nil)

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
