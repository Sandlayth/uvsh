#!/bin/sh
# Measure uvsh scanning a real directory: elapsed time, peak memory and bytes
# written to disk.
#
#   bench.sh UVSH_BINARY DIR [EXCLUDE_FILE] [RUNS] [DSN]
#
# Without DSN each run is a dry run. With DSN each run records into that
# catalog: run 1 writes everything, later runs measure an unchanged rescan.
# Run 1 usually also reads metadata from disk; later runs hit the OS cache.
set -eu

[ $# -ge 2 ] || { echo "usage: $0 UVSH_BINARY DIR [EXCLUDE_FILE] [RUNS] [DSN]" >&2; exit 2; }
UVSH=$1
DIR=$2
EXCLUDES=${3:-}
RUNS=${4:-3}
DSN=${5:-}

# measure ERRFILE CMD...: run CMD with stderr in ERRFILE and print its peak
# resident memory in KiB, sampled from the kernel's VmHWM high-water mark, and
# the bytes it caused to be written to storage (WAL and checkpoint included),
# from /proc/PID/io. Runs shorter than a few sampling intervals read low; n/a
# without /proc or the kernel's I/O accounting.
measure() {
	errfile=$1
	shift
	"$@" >/dev/null 2>"$errfile" &
	pid=$!
	hwm= written=
	while kill -0 "$pid" 2>/dev/null; do
		v=$(sed -n 's/^VmHWM:[^0-9]*\([0-9]*\).*/\1/p' "/proc/$pid/status" 2>/dev/null) || true
		[ -z "$v" ] || hwm=$v
		v=$(sed -n 's/^write_bytes: *\([0-9]*\).*/\1/p' "/proc/$pid/io" 2>/dev/null) || true
		[ -z "$v" ] || written=$v
		sleep 0.05 # fractional sleep: GNU and BusyBox, not strict POSIX
	done
	wait "$pid" || true # scan errors are reported in its summary
	echo "peak_rss_kib=${hwm:-n/a} written_kib=$(if [ -n "$written" ]; then echo $((written / 1024)); else echo n/a; fi)"
}

if [ -n "$DSN" ]; then set -- scan -db "$DSN"; else set -- scan -dry-run; fi
[ -z "$EXCLUDES" ] || set -- "$@" -exclude-from "$EXCLUDES"

err=$(mktemp)
trap 'rm -f "$err"' EXIT

echo "bench: $UVSH $* $DIR ($RUNS runs)"
i=1
while [ "$i" -le "$RUNS" ]; do
	m=$(measure "$err" "$UVSH" "$@" "$DIR")
	summary=$(sed -n 's/.*msg="scan complete" //p' "$err")
	echo "run=$i $summary $m"
	i=$((i + 1))
done
