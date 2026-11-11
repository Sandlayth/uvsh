package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
)

func openTest(t *testing.T) (*Catalog, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	c, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, path
}

// pinConns checks out n distinct connections at once, forcing the pool to
// open new ones; a pragma set via Exec instead of the DSN would be missing
// on all but one of them.
func pinConns(t *testing.T, db *sql.DB, n int) []*sql.Conn {
	t.Helper()
	conns := make([]*sql.Conn, n)
	for i := range conns {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		conns[i] = conn
	}
	return conns
}

func pragmaInt(t *testing.T, conn *sql.Conn, name string) int {
	t.Helper()
	var v int
	if err := conn.QueryRowContext(t.Context(), "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func TestOpenAppliesPragmasToEveryConnection(t *testing.T) {
	c, _ := openTest(t)

	pools := []struct {
		name string
		db   *sql.DB
		n    int
		want map[string]int
	}{
		{"writer", c.writer, 1, map[string]int{"busy_timeout": 5000, "foreign_keys": 1, "query_only": 0}},
		{"reader", c.reader, maxReaders, map[string]int{"busy_timeout": 5000, "foreign_keys": 1, "query_only": 1}},
	}
	for _, p := range pools {
		for i, conn := range pinConns(t, p.db, p.n) {
			for name, want := range p.want {
				if got := pragmaInt(t, conn, name); got != want {
					t.Errorf("%s conn %d: %s = %d, want %d", p.name, i, name, got, want)
				}
			}
		}
	}
}

func TestOpenMigratesToLatest(t *testing.T) {
	c, _ := openTest(t)

	info, err := c.Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if want := (catalog.Info{SchemaVersion: len(migrations)}); info != want {
		t.Errorf("Info = %+v, want %+v", info, want)
	}
	var mode string
	if err := c.reader.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	_, path := openTest(t)

	c, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer c.Close()
	info, err := c.Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.SchemaVersion != len(migrations) {
		t.Errorf("SchemaVersion = %d, want %d", info.SchemaVersion, len(migrations))
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	c, path := openTest(t)
	if _, err := c.writer.ExecContext(t.Context(), "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	_, err := Open(t.Context(), path)
	if !errors.Is(err, catalog.ErrSchemaTooNew) {
		t.Fatalf("Open error = %v, want ErrSchemaTooNew", err)
	}
}

func TestConcurrentOpenOfFreshCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() {
			c, err := Open(t.Context(), path)
			if err == nil {
				err = c.Close()
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Open #%d: %v", i, err)
		}
	}
}

func TestStrictAndReadOnlyEnforcement(t *testing.T) {
	c, _ := openTest(t)
	ctx := t.Context()
	mustExec(t, ctx, c.writer, "INSERT INTO roots (id, path, added_ns) VALUES (1, '/data', 0)")

	tests := []struct {
		name string
		db   *sql.DB
		stmt string
	}{
		{"strict rejects text in INTEGER column", c.writer, "INSERT INTO files (root_id, rel_path, size, mtime_ns) VALUES (1, 'a', 'big', 0)"},
		{"foreign key rejects unknown root", c.writer, "INSERT INTO files (root_id, rel_path, size, mtime_ns) VALUES (42, 'a', 1, 0)"},
		{"reader pool cannot write", c.reader, "INSERT INTO roots (path, added_ns) VALUES ('/other', 0)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.db.ExecContext(ctx, tt.stmt); err == nil {
				t.Errorf("statement succeeded, want error")
			}
		})
	}
}

func TestOpenRejectsBadPath(t *testing.T) {
	for _, path := range []string{"", "cat?alog.db"} {
		if _, err := Open(t.Context(), path); err == nil {
			t.Errorf("Open(%q) succeeded, want error", path)
		}
	}
}

func mustExec(t *testing.T, ctx context.Context, db *sql.DB, stmt string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}
