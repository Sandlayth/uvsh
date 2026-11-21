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
	"github.com/sandlayth/uvsh/internal/uvsh"
)

const (
	// No conflict target: it catches both (parent_id, name) and a root's one
	// top dir. The WHERE makes an unchanged entry a no-op (no page rewrite, no
	// WAL growth) that returns no row; IS NOT compares NULLs as values. An
	// entry back from missing is always written, to clear missing_since.
	sqlUpsert = `INSERT INTO entries (root_id, parent_id, name, kind, size, mtime_ns, target)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET kind = excluded.kind, size = excluded.size,
			mtime_ns = excluded.mtime_ns, target = excluded.target, missing_since = NULL
		WHERE entries.kind != excluded.kind OR entries.size IS NOT excluded.size
			OR entries.mtime_ns != excluded.mtime_ns OR entries.target IS NOT excluded.target
			OR entries.missing_since IS NOT NULL
		RETURNING id`
	sqlEntryID = `SELECT id FROM entries WHERE root_id = ? AND parent_id IS ? AND name = ?`

	// The seen table lives in the scan's connection, outside the WAL; %s is
	// its name. A root's top dir is never missing, so it isn't tracked.
	sqlSeenCreate = `CREATE TEMP TABLE %s (parent_id INTEGER NOT NULL, name TEXT NOT NULL,
		PRIMARY KEY (parent_id, name)) WITHOUT ROWID`
	sqlSeenAdd = `INSERT OR IGNORE INTO %s (parent_id, name) VALUES (?, ?)`
	// Skip keeps an entry and everything below it.
	sqlSeenSubtree = `INSERT OR IGNORE INTO %s (parent_id, name)
		WITH RECURSIVE sub(id, parent_id, name) AS (
			SELECT id, parent_id, name FROM entries WHERE parent_id = ? AND name = ?
			UNION ALL
			SELECT e.id, e.parent_id, e.name FROM entries e JOIN sub ON e.parent_id = sub.id)
		SELECT parent_id, name FROM sub`
	sqlSeenAll     = `INSERT OR IGNORE INTO %s (parent_id, name) SELECT parent_id, name FROM entries WHERE root_id = ? AND parent_id IS NOT NULL`
	sqlMarkMissing = `UPDATE entries SET missing_since = ?
		WHERE root_id = ? AND parent_id IS NOT NULL AND missing_since IS NULL
			AND NOT EXISTS (SELECT 1 FROM %s s WHERE s.parent_id = entries.parent_id AND s.name = entries.name)`
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

	// The seen table is per connection, so the scan keeps the writer's one
	// connection until it ends; the pool could otherwise close it when idle
	// and lose the table. Other writes wait meanwhile.
	conn, err := c.writer.Conn(ctx)
	if err != nil {
		return nil, err
	}
	w := &scanWriter{conn: conn, rootID: rootID, batchSize: opts.BatchSize, stats: catalog.ScanStats{
		Written: map[uvsh.Kind]int{}, Unchanged: map[uvsh.Kind]int{},
	}}
	err = conn.QueryRowContext(ctx, `INSERT INTO scans (root_id, started_ns) VALUES (?, ?) RETURNING id`,
		rootID, time.Now().UnixNano()).Scan(&w.scanID)
	if err == nil {
		w.seen = fmt.Sprintf("temp.seen_%d", w.scanID)
		_, err = conn.ExecContext(ctx, fmt.Sprintf(sqlSeenCreate, w.seen))
	}
	if err != nil {
		conn.Close()
		return nil, err
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
	conn      *sql.Conn // the writer's connection, held for the whole scan
	rootID    int64
	scanID    int64
	seen      string // name of the scan's seen table
	batchSize int

	tx      *sql.Tx // open batch; nil between batches
	stmts   batchStmts
	pending int // entries written in the open batch

	// ancestors holds the directories from the root down to the one the walk
	// is in. A depth-first walk only ever needs these to find a parent, so
	// memory follows the tree's depth, not its size.
	ancestors []dirRef

	stats catalog.ScanStats
	done  bool
}

// batchStmts are prepared on each batch's transaction: SQLite parses them
// once per batch, not per row.
type batchStmts struct {
	upsert, id, seenAdd *sql.Stmt
}

type dirRef struct {
	path string
	id   int64
}

func (w *scanWriter) Add(ctx context.Context, e uvsh.Entry) error {
	r := row{kind: e.Kind, mtime: e.ModTime.UnixNano()}
	switch e.Kind {
	case uvsh.Dir:
	case uvsh.File:
		r.size = sql.NullInt64{Int64: e.Size, Valid: true}
	case uvsh.Symlink:
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
	if err == nil && !written && e.Kind == uvsh.Dir {
		// The upsert returns no id when unchanged, but children need it.
		err = w.stmts.id.QueryRowContext(ctx, w.rootID, r.parent, r.name).Scan(&id)
	}
	if err != nil {
		return fmt.Errorf("%q: %w", e.Path, err)
	}
	if err == nil && r.parent.Valid {
		_, err = w.stmts.seenAdd.ExecContext(ctx, r.parent, r.name)
	}
	if err != nil {
		return fmt.Errorf("%q: %w", e.Path, err)
	}
	if written {
		w.stats.Written[e.Kind]++
	} else {
		w.stats.Unchanged[e.Kind]++
	}
	if e.Kind == uvsh.Dir {
		w.ancestors = append(w.ancestors, dirRef{e.Path, id})
	}
	return w.step()
}

// row is one entries row, minus root_id (the writer's) and id.
type row struct {
	parent sql.NullInt64 // invalid for the top dir
	name   string
	kind   uvsh.Kind
	size   sql.NullInt64
	mtime  int64
	target sql.NullString
}

// upsert reports whether r was inserted or changed, and its id if so.
func (w *scanWriter) upsert(ctx context.Context, r row) (id int64, written bool, err error) {
	if err := w.ensureTx(ctx); err != nil {
		return 0, false, err
	}
	err = w.stmts.upsert.QueryRowContext(ctx,
		w.rootID, r.parent, r.name, string(r.kind), r.size, r.mtime, r.target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (w *scanWriter) Skip(ctx context.Context, p string) error {
	if err := w.ensureTx(ctx); err != nil {
		return err
	}
	var err error
	if p == "." {
		_, err = w.tx.ExecContext(ctx, fmt.Sprintf(sqlSeenAll, w.seen), w.rootID)
	} else {
		var dirID int64
		if dirID, err = w.parentID(p); err == nil {
			_, err = w.tx.ExecContext(ctx, fmt.Sprintf(sqlSeenSubtree, w.seen), dirID, path.Base(p))
		}
	}
	if err != nil {
		return fmt.Errorf("skip %q: %w", p, err)
	}
	return nil
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
	tx, err := w.conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	for _, s := range []struct {
		stmt **sql.Stmt
		sql  string
	}{{&w.stmts.upsert, sqlUpsert}, {&w.stmts.id, sqlEntryID}, {&w.stmts.seenAdd, fmt.Sprintf(sqlSeenAdd, w.seen)}} {
		if *s.stmt, err = tx.PrepareContext(ctx, s.sql); err != nil {
			tx.Rollback()
			return err
		}
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

// commit ends the open batch; its statements close with it.
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

func (w *scanWriter) Finish(ctx context.Context, complete bool) (catalog.ScanStats, error) {
	if w.done {
		return w.stats, errors.New("scan writer already finished")
	}
	err := w.commit()
	if err == nil {
		err = w.close(ctx, complete)
	}
	w.release()
	return w.stats, err
}

// close marks what the scan didn't see as missing, if it was complete, and
// records the scan's end, in one transaction.
func (w *scanWriter) close(ctx context.Context, complete bool) error {
	tx, err := w.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if complete {
		res, err := tx.ExecContext(ctx, fmt.Sprintf(sqlMarkMissing, w.seen), w.scanID, w.rootID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		w.stats.Missing = int(n)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scans SET finished_ns = ?, complete = ? WHERE id = ?`,
		time.Now().UnixNano(), complete, w.scanID); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *scanWriter) Abort() {
	if w.done {
		return
	}
	if w.tx != nil {
		w.tx.Rollback()
		w.tx = nil
	}
	w.release()
}

// release drops the seen table and gives the connection back to the pool.
func (w *scanWriter) release() {
	w.done = true
	w.conn.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+w.seen)
	w.conn.Close()
}
