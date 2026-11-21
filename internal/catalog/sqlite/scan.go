package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"time"

	"github.com/sandlayth/uvsh/internal/catalog"
)

const (
	// No conflict target: it catches both (parent_id, name) and a root's one
	// top dir. The WHERE makes an unchanged entry a no-op (no page rewrite, no
	// WAL growth) that returns no row; IS NOT compares NULLs as values.
	sqlUpsert = `INSERT INTO entries (root_id, parent_id, name, kind, size, mtime_ns, target)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET kind = excluded.kind, size = excluded.size,
			mtime_ns = excluded.mtime_ns, target = excluded.target
		WHERE entries.kind != excluded.kind OR entries.size IS NOT excluded.size
			OR entries.mtime_ns != excluded.mtime_ns OR entries.target IS NOT excluded.target
		RETURNING id`
	sqlEntryID = `SELECT id FROM entries WHERE root_id = ? AND parent_id IS ? AND name = ?`
)

func (c *Catalog) BeginScan(ctx context.Context, root string, opts catalog.ScanOptions) (catalog.ScanWriter, error) {
	if opts.BatchSize < 1 {
		return nil, fmt.Errorf("batch size %d: must be at least 1", opts.BatchSize)
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("root %q: must be an absolute path", root)
	}
	rootID, err := c.registerRoot(ctx, filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("register root %s: %w", root, err)
	}

	w := &scanWriter{db: c.writer, rootID: rootID, batchSize: opts.BatchSize, stats: catalog.ScanStats{
		Written: map[catalog.Kind]int{}, Unchanged: map[catalog.Kind]int{},
	}}
	// Prepared once on the pool, then bound to each batch's transaction with
	// tx.StmtContext: SQLite parses each statement once per scan, not per row.
	for _, s := range []struct {
		stmt **sql.Stmt
		sql  string
	}{{&w.upsertStmt, sqlUpsert}, {&w.idStmt, sqlEntryID}} {
		if *s.stmt, err = c.writer.PrepareContext(ctx, s.sql); err != nil {
			w.closeStmts()
			return nil, err
		}
	}
	return w, nil
}

func (c *Catalog) registerRoot(ctx context.Context, root string) (int64, error) {
	tx, err := c.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM roots WHERE path = ?`, root).Scan(&id)
	if !errors.Is(err, sql.ErrNoRows) {
		return id, err // already registered, or a real error
	}
	// The write lock is held since BEGIN, so a concurrent scan can't register
	// an overlapping root between this check and the insert.
	registered, err := rootPathsTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := catalog.CheckRoot(root, registered); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO roots (path, added_ns) VALUES (?, ?) RETURNING id`,
		root, time.Now().UnixNano()).Scan(&id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func rootPathsTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT path FROM roots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

type scanWriter struct {
	db        *sql.DB
	rootID    int64
	batchSize int

	upsertStmt, idStmt *sql.Stmt

	tx      *sql.Tx // open batch; nil between batches
	pending int     // entries written in the open batch

	// ancestors holds the directories from the root down to the one the walk
	// is in. A depth-first walk only ever needs these to find a parent, so
	// memory follows the tree's depth, not its size.
	ancestors []dirRef

	stats catalog.ScanStats
	done  bool
}

type dirRef struct {
	path string
	id   int64
}

func (w *scanWriter) Add(ctx context.Context, e catalog.Entry) error {
	r := row{kind: e.Kind, mtime: e.ModTime.UnixNano()}
	switch e.Kind {
	case catalog.Dir:
	case catalog.File:
		r.size = sql.NullInt64{Int64: e.Size, Valid: true}
	case catalog.Symlink:
		r.target = sql.NullString{String: e.Target, Valid: true}
	default:
		return fmt.Errorf("%q: cannot record kind %q", e.Path, e.Kind)
	}
	if e.Path != "." {
		dirID, err := w.parentID(e.Path)
		if err != nil {
			return err
		}
		r.parent, r.name = sql.NullInt64{Int64: dirID, Valid: true}, path.Base(e.Path)
	}

	id, written, err := w.upsert(ctx, r)
	if err == nil && !written && e.Kind == catalog.Dir {
		// The upsert returns no id when unchanged, but children need it.
		err = w.tx.StmtContext(ctx, w.idStmt).QueryRowContext(ctx, w.rootID, r.parent, r.name).Scan(&id)
	}
	if err != nil {
		return fmt.Errorf("%q: %w", e.Path, err)
	}
	if written {
		w.stats.Written[e.Kind]++
	} else {
		w.stats.Unchanged[e.Kind]++
	}
	if e.Kind == catalog.Dir {
		w.ancestors = append(w.ancestors, dirRef{e.Path, id})
	}
	return w.step()
}

// row is one entries row, minus root_id (the writer's) and id.
type row struct {
	parent sql.NullInt64 // invalid for the top dir
	name   string
	kind   catalog.Kind
	size   sql.NullInt64
	mtime  int64
	target sql.NullString
}

// upsert reports whether r was inserted or changed, and its id if so.
func (w *scanWriter) upsert(ctx context.Context, r row) (id int64, written bool, err error) {
	if err := w.ensureTx(ctx); err != nil {
		return 0, false, err
	}
	err = w.tx.StmtContext(ctx, w.upsertStmt).QueryRowContext(ctx,
		w.rootID, r.parent, r.name, string(r.kind), r.size, r.mtime, r.target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// parentID pops ancestors the walk has left, then returns the parent of p.
func (w *scanWriter) parentID(p string) (int64, error) {
	parent := path.Dir(p)
	for len(w.ancestors) > 0 {
		top := w.ancestors[len(w.ancestors)-1]
		if top.path == parent {
			return top.id, nil
		}
		w.ancestors = w.ancestors[:len(w.ancestors)-1]
	}
	return 0, fmt.Errorf("%q: parent dir %q was not recorded first", p, parent)
}

func (w *scanWriter) ensureTx(ctx context.Context) error {
	if w.done {
		return errors.New("scan writer already finished")
	}
	if w.tx != nil {
		return nil
	}
	// database/sql rolls a transaction back as soon as its BeginTx ctx is
	// cancelled. The batch must outlive a Ctrl-C so Finish can still commit
	// what was walked; Abort ends it otherwise. Statements keep using ctx.
	tx, err := w.db.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	w.tx, w.pending = tx, 0
	return nil
}

func (w *scanWriter) step() error {
	w.pending++
	if w.pending < w.batchSize {
		return nil
	}
	return w.commit()
}

func (w *scanWriter) commit() error {
	if w.tx == nil {
		return nil
	}
	err := w.tx.Commit()
	w.tx = nil
	if err != nil {
		return err
	}
	w.stats.Batches++
	return nil
}

func (w *scanWriter) Finish(_ context.Context) (catalog.ScanStats, error) {
	if w.done {
		return w.stats, errors.New("scan writer already finished")
	}
	err := w.commit()
	w.done = true
	w.closeStmts()
	return w.stats, err
}

func (w *scanWriter) Abort() {
	if w.done {
		return
	}
	if w.tx != nil {
		w.tx.Rollback()
		w.tx = nil
	}
	w.done = true
	w.closeStmts()
}

func (w *scanWriter) closeStmts() {
	for _, s := range []*sql.Stmt{w.upsertStmt, w.idStmt} {
		if s != nil {
			s.Close()
		}
	}
}
