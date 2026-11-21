#!/bin/sh
# Build a deterministic directory tree for uvsh end-to-end checks.
#
#   mkfixture.sh DIR          create DIR (must be absent or empty)
#   mkfixture.sh -clean DIR   remove DIR, only if this script created it
#
# Layout:
#   DIR/tree/         the root to scan
#   DIR/outside/      targets that symlinks in tree/ must not escape to
#   DIR/excludes      sample -exclude-from file
#   DIR/expected      counts a correct scan must report, as key=value lines
#   DIR/.uvsh-fixture marker that makes -clean safe
#
# POSIX sh plus `head -c` (GNU coreutils and BusyBox both have it).
set -eu

# touch -t reads local time; pin it so every machine builds the same timestamps.
export TZ=UTC

MARKER=.uvsh-fixture
STAMP=202301151230.00 # touch -t format: CCYYMMDDhhmm.SS

usage() {
	echo "usage: $0 [-clean] DIR" >&2
	exit 2
}

clean() {
	[ -f "$1/$MARKER" ] || { echo "refusing to remove $1: no $MARKER marker" >&2; exit 1; }
	[ ! -d "$1/tree/locked" ] || chmod u+rwx "$1/tree/locked"
	rm -rf "$1"
}

# fill FILE SEED BYTES: deterministic content, distinct per seed.
fill() {
	yes "$2" | head -c "$3" >"$1"
	touch -t "$STAMP" "$1"
}

# count FIND-ARGS...: number of matches, robust to newlines in names.
count() {
	find "$@" -exec printf '.' \; | wc -c | tr -d ' '
}

[ $# -ge 1 ] || usage
if [ "$1" = -clean ]; then
	[ $# -eq 2 ] || usage
	clean "$2"
	exit 0
fi
[ $# -eq 1 ] || usage
DIR=$1

if [ -e "$DIR" ] && [ -n "$(ls -A "$DIR")" ]; then
	echo "refusing to use $DIR: not empty" >&2
	exit 1
fi
mkdir -p "$DIR"
: >"$DIR/$MARKER"
T=$DIR/tree

mkdir -p "$DIR/outside"
fill "$DIR/outside/secret.txt" outside 100

# Regular content.
mkdir -p "$T/docs" "$T/photos/2023/japan" "$T/photos/2024" "$T/big"
fill "$T/docs/readme.txt" readme 1000
: >"$T/docs/empty.txt"
touch -t "$STAMP" "$T/docs/empty.txt"
fill "$T/photos/2023/japan/img_0001.jpg" img-0001 65536
fill "$T/photos/2023/japan/img_0002.jpg" img-0002 65536
fill "$T/photos/2023/copy_of_0001.jpg" img-0001 65536 # same bytes as img_0001: dedup target
fill "$T/photos/2024/img_0001.jpg" img-2024 65536     # same name, different bytes
fill "$T/big/blob.bin" blob 8388608                   # 8 MiB: spans many hash chunks
fill "$T/big/epoch.bin" epoch 10
touch -t 197001010000.00 "$T/big/epoch.bin"

# Hostile names.
mkdir -p "$T/names"
fill "$T/names/with space.txt" space 10
fill "$T/names/unicode-é日本.txt" unicode 10
fill "$T/names/$(printf 'bad-\377-utf8.txt')" badutf8 10
fill "$T/names/$(printf 'new\nline.txt')" newline 10

mkdir -p "$T/deep/a/b/c/d/e/f/g/h"
fill "$T/deep/a/b/c/d/e/f/g/h/leaf.txt" leaf 10

# Symlinks: recorded with their target, never followed.
mkdir -p "$T/links"
ln -s ../docs/readme.txt "$T/links/to-file"
ln -s ../photos "$T/links/to-dir"
ln -s ../../outside/secret.txt "$T/links/to-outside"
ln -s /etc/passwd "$T/links/absolute"
ln -s ../nope.txt "$T/links/dangling"
ln -s chain-2 "$T/links/chain-1"
ln -s ../docs/readme.txt "$T/links/chain-2"
ln -s loop-b "$T/links/loop-a"
ln -s loop-a "$T/links/loop-b"

# Neither files nor symlinks: must be skipped (opening a FIFO blocks forever).
mkdir -p "$T/special"
mkfifo "$T/special/fifo"

# Noise for excludes: a cache dir at two depths, temp files, a name starting with '#'.
mkdir -p "$T/photos/2023/japan/.thumbnails" "$T/.thumbnails" "$T/#trash"
fill "$T/photos/2023/japan/.thumbnails/img_0001.jpg" thumb 100
fill "$T/.thumbnails/root.jpg" thumb 100
fill "$T/docs/~draft.tmp" tmp 10
fill "$T/#trash/old.txt" trash 10
cat >"$DIR/excludes" <<'EOF'
# One pattern per line. No '/' matches a name at any depth.
.thumbnails
*.tmp
\#trash
EOF

# Unreadable directory: an error for a normal user, readable for root.
mkdir -p "$T/locked"
fill "$T/locked/hidden.txt" hidden 10
chmod 000 "$T/locked"

# Counted with find, an implementation independent of uvsh.
excl='( -name .thumbnails -o -name #trash ) -prune -o'
{
	echo "user_is_root=$([ "$(id -u)" -eq 0 ] && echo 1 || echo 0)"
	echo "dirs=$(count "$T" -type d)" # includes the tree's root
	echo "files=$(count "$T" -type f)"
	echo "links=$(count "$T" -type l)"
	echo "other=$(count "$T" ! -type f ! -type d ! -type l)"
	# shellcheck disable=SC2086 # $excl is split on purpose
	echo "dirs_excluded=$(count "$T" $excl -type d)"
	# shellcheck disable=SC2086
	echo "files_excluded=$(count "$T" $excl -type f ! -name '*.tmp')"
} 2>/dev/null >"$DIR/expected"

echo "fixture ready in $DIR"
cat "$DIR/expected"
