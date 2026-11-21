-- Version 1. Until the first tagged release this file is edited in place;
-- after it, never edit a shipped migration: add the next numbered file.

CREATE TABLE roots (
	id       INTEGER PRIMARY KEY,
	path     TEXT    NOT NULL UNIQUE,
	added_ns INTEGER NOT NULL
) STRICT;

-- A tree rather than flat paths, so a directory's children are one indexed
-- range on (parent_id, name). Names are unique per directory, whatever their
-- kind, as on disk. root_id is on every row so per-root work needs no
-- recursive query.
-- One row per scan. complete is 1 only when the whole tree was walked: only
-- then can an entry the scan didn't see be called missing.
CREATE TABLE scans (
	id          INTEGER PRIMARY KEY,
	root_id     INTEGER NOT NULL REFERENCES roots(id),
	started_ns  INTEGER NOT NULL,
	finished_ns INTEGER,                    -- NULL while running or if aborted
	complete    INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1))
) STRICT;

CREATE TABLE entries (
	id        INTEGER PRIMARY KEY,
	root_id   INTEGER NOT NULL REFERENCES roots(id),
	parent_id INTEGER REFERENCES entries(id), -- NULL for a root's top dir
	name      TEXT    NOT NULL,               -- '' for a root's top dir
	kind      TEXT    NOT NULL CHECK (kind IN ('dir', 'file', 'link')),
	size      INTEGER,                        -- files only
	mtime_ns  INTEGER NOT NULL,
	target    TEXT,                           -- links only, exactly what readlink returned
	-- Rows are never deleted: an entry gone from disk is marked by the
	-- complete scan that first missed it, and cleared if it comes back.
	missing_since INTEGER REFERENCES scans(id),
	UNIQUE (parent_id, name),
	CHECK ((kind = 'file') = (size IS NOT NULL)),
	CHECK ((kind = 'link') = (target IS NOT NULL)),
	CHECK (parent_id IS NOT NULL OR (kind = 'dir' AND name = ''))
) STRICT;

-- UNIQUE treats NULLs as distinct, so (parent_id, name) alone would allow
-- several top dirs per root.
CREATE UNIQUE INDEX entries_one_top ON entries (root_id) WHERE parent_id IS NULL;
