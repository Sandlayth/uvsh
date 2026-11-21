package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
)

// Each branch seeks its (parent, name) index and stops after :n rows, so at
// most 3n rows are merged however large the directory is. name >= :name is
// the index range; the OR then drops the cursor row itself.
const sqlList = `
SELECT name, kind, size, mtime_ns, target FROM (
	SELECT * FROM (
		SELECT name, 0 AS kind, 0 AS size, mtime_ns, '' AS target FROM dirs
		WHERE parent_id = :dir AND name >= :name AND (name > :name OR 0 > :kind)
		ORDER BY name LIMIT :n)
	UNION ALL
	SELECT * FROM (
		SELECT name, 1, size, mtime_ns, '' FROM files
		WHERE dir_id = :dir AND name >= :name AND (name > :name OR 1 > :kind)
		ORDER BY name LIMIT :n)
	UNION ALL
	SELECT * FROM (
		SELECT name, 2, 0, mtime_ns, target FROM links
		WHERE dir_id = :dir AND name >= :name AND (name > :name OR 2 > :kind)
		ORDER BY name LIMIT :n)
)
ORDER BY name, kind LIMIT :n`

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

func (c *Catalog) Stat(ctx context.Context, path string) (catalog.Entry, error) {
	tx, err := c.reader.BeginTx(ctx, nil)
	if err != nil {
		return catalog.Entry{}, err
	}
	defer tx.Rollback()
	n, err := catalog.Resolve(ctx, txLookup{tx}, path)
	return n.Entry, err
}

func (c *Catalog) List(ctx context.Context, path string, after catalog.Cursor, limit int) ([]catalog.Entry, error) {
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
	if dir.Kind != catalog.Dir {
		return nil, fmt.Errorf("%s: %w", path, catalog.ErrNotDir)
	}

	rows, err := tx.QueryContext(ctx, sqlList, sql.Named("dir", dir.ID), sql.Named("name", after.Name),
		sql.Named("kind", int(after.Kind)), sql.Named("n", limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]catalog.Entry, 0, limit)
	for rows.Next() {
		var e catalog.Entry
		var mtime int64
		if err := rows.Scan(&e.Name, &e.Kind, &e.Size, &mtime, &e.Target); err != nil {
			return nil, err
		}
		e.ModTime = time.Unix(0, mtime)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// txLookup serves catalog.Resolve from one read transaction.
type txLookup struct{ tx *sql.Tx }

func (l txLookup) Root(ctx context.Context, path string) (catalog.Node, bool, error) {
	n := catalog.Node{Entry: catalog.Entry{Kind: catalog.Dir}}
	var mtime int64
	err := l.tx.QueryRowContext(ctx, `SELECT d.id, d.mtime_ns FROM roots r
		JOIN dirs d ON d.root_id = r.id AND d.parent_id IS NULL WHERE r.path = ?`, path).Scan(&n.ID, &mtime)
	return found(n, mtime, err)
}

func (l txLookup) Child(ctx context.Context, dir catalog.Node, name string) (catalog.Node, bool, error) {
	n := catalog.Node{Entry: catalog.Entry{Name: name, Kind: catalog.Dir}}
	var mtime int64
	err := l.tx.QueryRowContext(ctx, `SELECT id, mtime_ns FROM dirs WHERE parent_id = ? AND name = ?`, dir.ID, name).Scan(&n.ID, &mtime)
	if !errors.Is(err, sql.ErrNoRows) {
		return found(n, mtime, err)
	}
	n.Kind = catalog.File
	err = l.tx.QueryRowContext(ctx, `SELECT id, size, mtime_ns FROM files WHERE dir_id = ? AND name = ?`, dir.ID, name).Scan(&n.ID, &n.Size, &mtime)
	if !errors.Is(err, sql.ErrNoRows) {
		return found(n, mtime, err)
	}
	n.Kind = catalog.Symlink
	err = l.tx.QueryRowContext(ctx, `SELECT id, target, mtime_ns FROM links WHERE dir_id = ? AND name = ?`, dir.ID, name).Scan(&n.ID, &n.Target, &mtime)
	return found(n, mtime, err)
}

func found(n catalog.Node, mtime int64, err error) (catalog.Node, bool, error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return catalog.Node{}, false, nil
	case err != nil:
		return catalog.Node{}, false, err
	}
	n.ModTime = time.Unix(0, mtime)
	return n, true, nil
}
