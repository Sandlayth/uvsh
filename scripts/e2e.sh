#!/bin/sh
# Run uvsh against a fixture built by mkfixture.sh: catalog lifecycle checks,
# then scan counts compared with the fixture's expected file.
#
#   e2e.sh UVSH_BINARY FIXTURE_DIR
set -eu

[ $# -eq 2 ] || { echo "usage: $0 UVSH_BINARY FIXTURE_DIR" >&2; exit 2; }
UVSH=$1
DIR=$2
. "$DIR/expected" # defines user_is_root, dirs, files, symlinks, other, dirs_excluded, files_excluded
errors=$((1 - user_is_root)) # the locked dir is only unreadable for non-root

failures=0

# check NAME WANT SUMMARY KEY
check() {
	got=$(printf '%s\n' "$3" | sed -n "s/.* $4=\([0-9]*\).*/\1/p")
	if [ "$got" = "$2" ]; then
		echo "ok   $1: $4=$got"
	else
		echo "FAIL $1: $4=$got, want $2"
		failures=$((failures + 1))
	fi
}

# expect NAME CMD...: run CMD, record ok/FAIL by its exit status.
expect() {
	name=$1
	shift
	if "$@"; then
		echo "ok   $name"
	else
		echo "FAIL $name"
		failures=$((failures + 1))
	fi
}

has_schema() { printf '%s\n' "$1" | grep -q 'schema v[0-9]'; }
no_wal_files() { [ ! -e "$1-wal" ] && [ ! -e "$1-shm" ]; }
fails() { ! "$@" >/dev/null 2>&1; }

# Catalog lifecycle. Files live in the fixture dir so -clean removes them.
db=$DIR/catalog.db
out=$("$UVSH" init -db "sqlite:$db" 2>&1) || true
expect "init creates catalog" has_schema "$out"
out=$("$UVSH" init -db "sqlite:$db" 2>&1) || true
expect "init reopens catalog" has_schema "$out"
expect "no WAL files left after close" no_wal_files "$db"

race=$DIR/race.db
i=1
while [ "$i" -le 6 ]; do
	"$UVSH" init -db "sqlite:$race" >"$DIR/race.$i.out" 2>&1 &
	i=$((i + 1))
done
wait
ok_inits=$(grep -l 'schema v[0-9]' "$DIR"/race.*.out | wc -l | tr -d ' ')
rm -f "$DIR"/race.*.out
expect "6 concurrent inits on a fresh catalog ($ok_inits ok)" [ "$ok_inits" = 6 ]

expect "unsupported backend rejected" fails "$UVSH" init -db nosuch:x
expect "dsn without backend rejected" fails "$UVSH" init -db "$db"

summary=$("$UVSH" scan -dry-run "$DIR/tree" 2>&1 >/dev/null | grep 'scan complete' || true)
echo "$summary"
check "full scan" "$dirs" "$summary" dirs
check "full scan" "$files" "$summary" files
check "full scan" "$symlinks" "$summary" symlinks
check "full scan" "$other" "$summary" other
check "full scan" "$errors" "$summary" errors

summary=$("$UVSH" scan -dry-run -exclude-from "$DIR/excludes" "$DIR/tree" 2>&1 >/dev/null | grep 'scan complete' || true)
echo "$summary"
check "with excludes" "$dirs_excluded" "$summary" dirs
check "with excludes" "$files_excluded" "$summary" files

# Recording into the catalog: the first scan writes every dir and file, an
# unchanged rescan writes nothing.
summary=$("$UVSH" scan -db "sqlite:$db" -batch 5 "$DIR/tree" 2>&1 >/dev/null | grep 'scan complete' || true)
echo "$summary"
check "record" "$dirs" "$summary" dirs_written
check "record" "$files" "$summary" files_written
check "record" "$errors" "$summary" errors

summary=$("$UVSH" scan -db "sqlite:$db" -batch 5 "$DIR/tree" 2>&1 >/dev/null | grep 'scan complete' || true)
echo "$summary"
check "rescan" 0 "$summary" dirs_written
check "rescan" 0 "$summary" files_written
check "rescan" "$files" "$summary" files_unchanged

[ "$failures" -eq 0 ] || { echo "$failures check(s) failed" >&2; exit 1; }
echo "all checks passed"
