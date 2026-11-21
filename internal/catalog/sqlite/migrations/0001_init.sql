-- Version 1. Until the first tagged release this file is edited in place;
-- after it, never edit a shipped migration: add the next numbered file.

CREATE TABLE roots (
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
) STRICT;

-- target is exactly what readlink returned; it is never followed at scan time.
CREATE TABLE links (
	id       INTEGER PRIMARY KEY,
	dir_id   INTEGER NOT NULL REFERENCES dirs(id),
	name     TEXT    NOT NULL,
	target   TEXT    NOT NULL,
	mtime_ns INTEGER NOT NULL,
	UNIQUE (dir_id, name)
) STRICT;
