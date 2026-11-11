package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/sandlayth/uvsh/internal/catalog"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrations[i] upgrades the schema from version i to i+1
var migrations = func() []string {
	m, err := loadMigrations(migrationFS)
	if err != nil {
		panic(err)
	}
	return m
}()

func loadMigrations(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, "migrations") // sorted by name
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		num, rest, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || len(num) != 4 || rest == ".sql" || !strings.HasSuffix(rest, ".sql") {
			return nil, fmt.Errorf("migration %q: want NNNN_name.sql", name)
		}
		if v != len(out)+1 {
			return nil, fmt.Errorf("migration %q: want version %04d next", name, len(out)+1)
		}
		b, err := fs.ReadFile(fsys, path.Join("migrations", name))
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	return out, nil
}

// migrate applies pending migrations one transaction at a time. The version
// is read inside the transaction (which takes the write lock immediately), so
// two processes opening a fresh catalog at once can't apply the same step twice.
func migrate(ctx context.Context, db *sql.DB) error {
	for {
		done, err := migrateStep(ctx, db)
		if err != nil || done {
			return err
		}
	}
}

func migrateStep(ctx context.Context, db *sql.DB) (done bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() // no-op after Commit

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return false, fmt.Errorf("read schema version: %w", err)
	}
	switch {
	case version > len(migrations):
		return false, fmt.Errorf("%w: database is v%d, binary knows up to v%d", catalog.ErrSchemaTooNew, version, len(migrations))
	case version == len(migrations):
		return true, nil
	}

	if _, err := tx.ExecContext(ctx, migrations[version]); err != nil {
		return false, fmt.Errorf("v%d: %w", version+1, err)
	}
	// PRAGMA takes no bound parameters; version is an int we control.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version+1)); err != nil {
		return false, fmt.Errorf("v%d: %w", version+1, err)
	}
	return false, tx.Commit()
}
