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
	sqlDirGet = `SELECT id, mtime_ns FROM dirs WHERE root_id = ? AND parent_id IS ? AND name = ?`
	sqlDirIns = `INSERT INTO dirs (root_id, parent_id, name, mtime_ns) VALUES (?, ?, ?, ?) RETURNING id`
	sqlDirUpd = `UPDATE dirs SET mtime_ns = ? WHERE id = ?`
	// The WHERE makes an unchanged file a no-op: no page rewrite, no WAL growth.
	sqlFileUpsert = `INSERT INTO files (dir_id, name, size, mtime_ns) VALUES (?, ?, ?, ?)
		ON CONFLICT (dir_id, name) DO UPDATE SET size = excluded.size, mtime_ns = excluded.mtime_ns
		WHERE files.size != excluded.size OR files.mtime_ns != excluded.mtime_ns`
	sqlLinkUpsert = `INSERT INTO links (dir_id, name, target, mtime_ns) VALUES (?, ?, ?, ?)
		ON CONFLICT (dir_id, name) DO UPDATE SET target = excluded.target, mtime_ns = excluded.mtime_ns
		WHERE links.target != excluded.target OR links.mtime_ns != excluded.mtime_ns`
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

	w := &scanWriter{db: c.writer, rootID: rootID, batchSize: opts.BatchSize}
	// Prepared once on the pool, then bound to each batch's transaction with
	// tx.StmtContext: SQLite parses each statement once per scan, not per row.
	for _, s := range []struct {
		stmt **sql.Stmt
		sql  string
	}{{&w.dirGet, sqlDirGet}, {&w.dirIns, sqlDirIns}, {&w.dirUpd, sqlDirUpd}, {&w.fileUpsert, sqlFileUpsert}, {&w.linkUpsert, sqlLinkUpsert}} {
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

	dirGet, dirIns, dirUpd, fileUpsert, linkUpsert *sql.Stmt

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

func (w *scanWriter) AddDir(ctx context.Context, d catalog.DirRecord) error {
	var parent sql.NullInt64
	name := ""
	if d.Path != "." {
		id, err := w.parentID(d.Path)
		if err != nil {
			return err
		}
		parent = sql.NullInt64{Int64: id, Valid: true}
		name = path.Base(d.Path)
	}
	if err := w.ensureTx(ctx); err != nil {
		return err
	}

	mtime := d.ModTime.UnixNano()
	var id, stored int64
	err := w.tx.StmtContext(ctx, w.dirGet).QueryRowContext(ctx, w.rootID, parent, name).Scan(&id, &stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = w.tx.StmtContext(ctx, w.dirIns).QueryRowContext(ctx, w.rootID, parent, name, mtime).Scan(&id)
		w.stats.DirsWritten++
	case err == nil && stored != mtime:
		_, err = w.tx.StmtContext(ctx, w.dirUpd).ExecContext(ctx, mtime, id)
		w.stats.DirsWritten++
	case err == nil:
		w.stats.DirsUnchanged++
	}
	if err != nil {
		return fmt.Errorf("dir %q: %w", d.Path, err)
	}
	w.ancestors = append(w.ancestors, dirRef{d.Path, id})
	return w.step()
}

func (w *scanWriter) AddFile(ctx context.Context, f catalog.FileRecord) error {
	return w.upsert(ctx, w.fileUpsert, f.Path, &w.stats.FilesWritten, &w.stats.FilesUnchanged, f.Size, f.ModTime.UnixNano())
}

func (w *scanWriter) AddLink(ctx context.Context, l catalog.LinkRecord) error {
	return w.upsert(ctx, w.linkUpsert, l.Path, &w.stats.LinksWritten, &w.stats.LinksUnchanged, l.Target, l.ModTime.UnixNano())
}

// upsert runs a conditional upsert of the entry at p with values after its
// (dir_id, name), and counts it as written or unchanged.
func (w *scanWriter) upsert(ctx context.Context, stmt *sql.Stmt, p string, written, unchanged *int, values ...any) error {
	dirID, err := w.parentID(p)
	if err != nil {
		return err
	}
	if err := w.ensureTx(ctx); err != nil {
		return err
	}
	res, err := w.tx.StmtContext(ctx, stmt).ExecContext(ctx, append([]any{dirID, path.Base(p)}, values...)...)
	if err != nil {
		return fmt.Errorf("%q: %w", p, err)
	}
	// 1 when inserted or changed, 0 when the upsert's WHERE found it unchanged.
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n > 0 {
		*written++
	} else {
		*unchanged++
	}
	return w.step()
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
	for _, s := range []*sql.Stmt{w.dirGet, w.dirIns, w.dirUpd, w.fileUpsert, w.linkUpsert} {
		if s != nil {
			s.Close()
		}
	}
}
