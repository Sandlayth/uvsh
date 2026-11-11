#!/bin/sh
# Measure uvsh scanning a real directory: elapsed time and peak memory.
#
#   bench.sh UVSH_BINARY DIR [EXCLUDE_FILE] [RUNS]
#
# Run 1 usually reads metadata from disk; later runs hit the OS cache.
set -eu

[ $# -ge 2 ] || { echo "usage: $0 UVSH_BINARY DIR [EXCLUDE_FILE] [RUNS]" >&2; exit 2; }
UVSH=$1
DIR=$2
EXCLUDES=${3:-}
RUNS=${4:-3}

# measure ERRFILE CMD...: run CMD with stderr in ERRFILE and print its peak
# resident memory in KiB, sampled from the kernel's VmHWM high-water mark.
# Runs shorter than a few sampling intervals read low; n/a without /proc.
measure() {
	errfile=$1
	shift
	"$@" >/dev/null 2>"$errfile" &
	pid=$!
	hwm=
	while kill -0 "$pid" 2>/dev/null; do
		v=$(sed -n 's/^VmHWM:[^0-9]*\([0-9]*\).*/\1/p' "/proc/$pid/status" 2>/dev/null) || true
		[ -z "$v" ] || hwm=$v
		sleep 0.05 # fractional sleep: GNU and BusyBox, not strict POSIX
	done
	wait "$pid" || true # scan errors are reported in its summary
	echo "${hwm:-n/a}"
}

set -- scan -dry-run
[ -z "$EXCLUDES" ] || set -- "$@" -exclude-from "$EXCLUDES"

err=$(mktemp)
trap 'rm -f "$err"' EXIT

echo "bench: $UVSH $* $DIR ($RUNS runs)"
i=1
while [ "$i" -le "$RUNS" ]; do
	rss=$(measure "$err" "$UVSH" "$@" "$DIR")
	summary=$(sed -n 's/.*msg="scan complete" //p' "$err")
	echo "run=$i $summary peak_rss_kib=$rss"
	i=$((i + 1))
done
