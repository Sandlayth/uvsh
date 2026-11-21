# UVSH — Unified Virtual Storage Hub

UVSH is a headless virtual filesystem and metadata catalog for the files you
already have. It walks your storage and records what is there in a catalog
you can browse, without moving or modifying a single file.

> **Status: early development.** The catalog and its command-line tools work
> and are tested, but there is no release yet and the on-disk format may
> still change.

## Principles

- **Never touch the data.** UVSH only reads the storage it indexes;
  everything it knows lives in its own catalog.
- **Browse without touching the disks.** Listing a directory is answered
  from the catalog; the scanned storage can sleep.
- **Constant memory.** Scanning, listing and resolving use memory that depends
  on the depth of a tree, never on how many files it holds.
- **Defaults that run anywhere.** Limits that only matter on some machines
  are options with sensible defaults, not assumptions in the code.

## Commands at a glance

| | |
|---|---|
| `uvsh init` | Create a catalog, or upgrade its schema. |
| `uvsh scan` | Walk a directory and record every dir, file and symlink in the catalog. Rescans only write what changed; entries that disappeared are marked missing, never deleted. |
| `uvsh scan -dry-run` | Walk and print, without a catalog. |
| `uvsh ls` | Browse the catalog: a directory, a whole tree (`-R`), or the registered roots. Symlinks show what they resolve to. |
| `uvsh version` | Build information. |

## Quick start

Requires Go 1.27 or later. No C compiler: the binary is pure Go.

```sh
make build                                   # bin/uvsh
bin/uvsh init -db sqlite:$HOME/uvsh.db
bin/uvsh scan -db sqlite:$HOME/uvsh.db ~/Pictures
bin/uvsh ls   -db sqlite:$HOME/uvsh.db ~/Pictures/2023
```

`ls` prints one tab-separated line per entry: kind, size, modification time
(UTC), name. Links add their target and what it resolves to:

```
dir	-	2023-08-14T09:12:40Z	Holidays
file	2481733	2023-08-14T09:12:41Z	IMG_0001.jpg
link	-	2024-01-02T18:00:00Z	best -> Holidays/IMG_0042.jpg	[file]
```

## Commands

Every command that uses a catalog takes `-db <backend>:<location>`. The only
backend today is `sqlite`, e.g. `-db sqlite:/data/uvsh/catalog.db`. The global
flag `-log-level debug|info|warn|error` goes before the command.

Exit status: 0 on success, 1 on failure (including a scan that skipped
unreadable entries), 2 on a usage error.

### `uvsh scan (-db dsn | -dry-run) [-batch n] [-exclude-from file] dir`

Walks `dir` without following symlinks. `dir` is registered as a **root** under
its real path (symlinks in it resolved). A root can't be inside another root,
or contain one.

- `-batch n`: entries written per transaction (default 1000).
- `-exclude-from file`: one pattern per line, `#` starts a comment. A pattern
  without `/` matches a name at any depth (`*.tmp`, `.cache`); with `/` it
  matches the path from the root (`photos/raw`). Patterns follow Go's
  `path.Match`; `\#recycle` matches a name starting with `#`.

A summary goes to stderr: entries seen per kind, entries written or unchanged,
entries newly missing, transactions, elapsed time. Ctrl-C stops the walk and
still commits what was walked. Entries are only marked missing after a scan
that walked the whole tree, and never below a directory that couldn't be read.

### `uvsh ls -db dsn [-page n] [-after name] [-limit n] [path]`

Lists the directory at `path`, in byte order of names. Without a path, lists
the registered roots. `-R` lists everything below `path`, depth first, in the
format of `scan -dry-run`.

- `-page n`: entries fetched per catalog query (default 1000). Memory is one
  page per open directory, whatever the directory's size.
- `-after name`: start after the entry named `name`.
- `-limit n`: stop after `n` entries.

A symlink's last column says what opening it would reach, judged from the
catalog and from the link's own root only: `[file]`, `[dir]`, `[dangling]`,
`[loop]` (more than 40 links, as the Linux kernel counts them) or `[outside]`
(outside the root, possibly inside another one). `ls` itself never follows
links.

### `uvsh init -db dsn`

Creates the catalog if needed and applies schema migrations. A catalog made by
a newer uvsh is refused rather than misread.

## Development

```sh
make check    # gofmt, go vet, go test -race, and a cgo-free build: run before every commit
make e2e      # build a fixture tree (hostile names, links, loops, a fifo...) and check uvsh against it
make bench BENCH_DIR=/some/dir [BENCH_DB=sqlite:/tmp/bench.db] [BENCH_RUNS=3]
```

`make bench` reports, per run: entries seen and written, elapsed time, peak
resident memory and bytes written to disk. Without `BENCH_DB` runs are dry
runs; with it, run 1 records everything and later runs measure an unchanged
rescan.

### Testing on other machines

`cross.mk` builds for other platforms and runs the same checks remotely over
ssh. Copy `local.mk.example` to `local.mk` (untracked) and list your machines
in `DEPLOY_HOST`; each one's platform is detected.

```sh
make -f cross.mk cross          # build for TARGET_GOOS/GOARCH (default linux/arm/v7)
make -f cross.mk e2e            # deploy, rebuild the fixture, run the e2e checks on every host
make -f cross.mk test-remote    # run the unit tests on every host
make -f cross.mk bench          # the bench, on every host
```

## License

MIT, see [LICENSE](LICENSE).
