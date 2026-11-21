// Package sqlite stores the catalog in an embedded SQLite database in WAL mode.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	driver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	maxReaders  = 4
	busyTimeout = 5 * time.Second
)

// Applied by the driver to every new connection (pragmas are per connection).
var commonPragmas = []string{
	fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()),
	"foreign_keys(1)", // enforce REFERENCES; SQLite leaves this off by default
}

type Catalog struct {
	writer *sql.DB
	reader *sql.DB
}

func Open(ctx context.Context, path string) (*Catalog, error) {
	if path == "" || strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("invalid catalog path %q", path)
	}

	// One connection: writers queue in Go instead of failing with SQLITE_BUSY.
	writer, err := openPool(path, 1, url.Values{
		// BEGIN IMMEDIATE takes the write lock up front, avoiding the read-to-write
		// upgrade deadlock that busy_timeout cannot resolve.
		"_txlock": {"immediate"},
		"_pragma": commonPragmas,
	})
	if err != nil {
		return nil, err
	}
	if err := enableWAL(ctx, writer); err != nil {
		writer.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := migrate(ctx, writer); err != nil {
		writer.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}

	reader, err := openPool(path, maxReaders, url.Values{
		"_pragma": slices.Concat(commonPragmas, []string{
			"query_only(1)", // any write through the reader pool fails
		}),
	})
	if err != nil {
		writer.Close()
		return nil, err
	}
	if err := reader.PingContext(ctx); err != nil {
		reader.Close()
		writer.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return &Catalog{writer: writer, reader: reader}, nil
}

// SQLite doesn't run the busy handler for this switch, so concurrent creation
// of a fresh catalog fails immediately with SQLITE_BUSY; retry instead.
func enableWAL(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(busyTimeout)
	for {
		var mode string
		err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode)
		switch {
		case err == nil && mode == "wal":
			return nil
		case err == nil:
			return fmt.Errorf("journal mode is %q, want wal", mode)
		case !isBusy(err) || time.Now().After(deadline):
			return fmt.Errorf("enable WAL: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func isBusy(err error) bool {
	var serr *driver.Error
	// The low byte is the primary result code; the rest marks extended codes.
	return errors.As(err, &serr) && serr.Code()&0xff == sqlite3.SQLITE_BUSY
}

func openPool(path string, maxConns int, params url.Values) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?"+params.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	// Each idle connection keeps its own page cache; give the RAM back when quiet.
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}

// Close closes both pools. Readers go first so the writer's close can
// checkpoint the WAL back into the main file.
func (c *Catalog) Close() error {
	return errors.Join(c.reader.Close(), c.writer.Close())
}

// Info reports the schema version.
func (c *Catalog) Info(ctx context.Context) (catalog.Info, error) {
	var info catalog.Info
	if err := c.reader.QueryRowContext(ctx, "PRAGMA user_version").Scan(&info.SchemaVersion); err != nil {
		return catalog.Info{}, fmt.Errorf("read schema version: %w", err)
	}
	return info, nil
}
