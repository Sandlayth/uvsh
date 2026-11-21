package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
	"github.com/sandlayth/uvsh/internal/uvsh"
)

// One seek on the (parent_id, name) index, then rows in order until :n are
// present: missing entries are kept in the catalog but never listed.
// Names are unique per dir, so the cursor's name alone is enough.
const sqlList = `SELECT ` + entryCols + ` FROM entries
	WHERE parent_id = :dir AND name > :name AND missing_since IS NULL ORDER BY name LIMIT :n`

const entryCols = `id, name, kind, coalesce(size, 0), mtime_ns, coalesce(target, '')`

func (c *Catalog) Roots(ctx context.Context) ([]catalog.Root, error) {
	rows, err := c.reader.QueryContext(ctx, `SELECT path, added_ns FROM roots ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []catalog.Root
	for rows.Next() {
		var r catalog.Root
		var added int64
		if err := rows.Scan(&r.Path, &added); err != nil {
			return nil, err
		}
		r.Added = time.Unix(0, added)
		roots = append(roots, r)
	}
	return roots, rows.Err()
}

func (c *Catalog) Stat(ctx context.Context, path string) (uvsh.Entry, error) {
	tx, err := c.reader.BeginTx(ctx, nil)
	if err != nil {
		return uvsh.Entry{}, err
	}
	defer tx.Rollback()
	n, err := catalog.Resolve(ctx, txLookup{tx}, path)
	return n.Entry, err
}

func (c *Catalog) List(ctx context.Context, path string, after string, limit int) ([]uvsh.Entry, error) {
	if limit < 1 {
		return nil, fmt.Errorf("limit %d: must be at least 1", limit)
	}
	// Resolve and list in one snapshot, so the dir can't vanish in between.
	tx, err := c.reader.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	dir, err := catalog.Resolve(ctx, txLookup{tx}, path)
	if err != nil {
		return nil, err
	}
	if dir.Kind != uvsh.Dir {
		return nil, fmt.Errorf("%s: %w", path, catalog.ErrNotDir)
	}

	rows, err := tx.QueryContext(ctx, sqlList, sql.Named("dir", dir.ID), sql.Named("name", after), sql.Named("n", limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]uvsh.Entry, 0, limit)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, n.Entry)
	}
	return entries, rows.Err()
}

func (c *Catalog) Read(ctx context.Context, fn func(catalog.Lookup) error) error {
	tx, err := c.reader.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(txLookup{tx})
}

// txLookup serves lookups from one read transaction.
type txLookup struct{ tx *sql.Tx }

func (l txLookup) Root(ctx context.Context, path string) (catalog.Node, bool, error) {
	return found(scanNode(l.tx.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries
		WHERE parent_id IS NULL AND root_id = (SELECT id FROM roots WHERE path = ?)`, path)))
}

func (l txLookup) Child(ctx context.Context, dir catalog.Node, name string) (catalog.Node, bool, error) {
	return found(scanNode(l.tx.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries
		WHERE parent_id = ? AND name = ? AND missing_since IS NULL`, dir.ID, name)))
}

// scanNode reads entryCols from a *sql.Row or *sql.Rows.
func scanNode(r interface{ Scan(...any) error }) (catalog.Node, error) {
	var n catalog.Node
	var mtime int64
	if err := r.Scan(&n.ID, &n.Path, &n.Kind, &n.Size, &mtime, &n.Target); err != nil {
		return catalog.Node{}, err
	}
	switch n.Kind {
	case uvsh.Dir, uvsh.File, uvsh.Symlink:
	default:
		return catalog.Node{}, fmt.Errorf("entry %d: unknown kind %q", n.ID, n.Kind)
	}
	n.ModTime = time.Unix(0, mtime)
	return n, nil
}

// found turns "no row" into ok == false.
func found(n catalog.Node, err error) (catalog.Node, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Node{}, false, nil
	}
	return n, err == nil, err
}
