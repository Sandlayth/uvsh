package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sandlayth/uvsh/internal/catalog"
)

// migrations[i] upgrades the schema from version i to i+1. Once a release has
// shipped a migration, never edit it: append a new one. Until the first
// tagged release no user database exists, so v1 is still edited in place.
var migrations = []string{
	`CREATE TABLE roots (
		id       INTEGER PRIMARY KEY,
		path     TEXT    NOT NULL UNIQUE,
		added_ns INTEGER NOT NULL
	) STRICT;

	-- A tree rather than flat paths, so a directory's direct children are one
	-- indexed range: (parent_id, name) for dirs, (dir_id, name) for files.
	CREATE TABLE dirs (
		id        INTEGER PRIMARY KEY,
		root_id   INTEGER NOT NULL REFERENCES roots(id),
		parent_id INTEGER REFERENCES dirs(id), -- NULL for the root dir
		name      TEXT    NOT NULL,            -- '' for the root dir
		mtime_ns  INTEGER NOT NULL,
		UNIQUE (parent_id, name)
	) STRICT;

	-- UNIQUE treats NULLs as distinct, so (parent_id, name) alone would allow
	-- several root dirs per root.
	CREATE UNIQUE INDEX dirs_one_root ON dirs (root_id) WHERE parent_id IS NULL;

	CREATE TABLE files (
		id       INTEGER PRIMARY KEY,
		dir_id   INTEGER NOT NULL REFERENCES dirs(id),
		name     TEXT    NOT NULL,
		size     INTEGER NOT NULL,
		mtime_ns INTEGER NOT NULL,
		UNIQUE (dir_id, name)
	) STRICT;`,
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
