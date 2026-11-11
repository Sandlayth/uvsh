#!/bin/sh
# Run a development action on every host in DEPLOY_HOST. Run from the repo root.
#
#   remote.sh deploy|smoke|fixture|e2e|test-remote|bench
#
# DEPLOY_HOST is a comma-separated list of user@host:/runtime/dir:/scan/dir
#   runtime dir  the uvsh binary, helper scripts and, briefly, compiled tests
#   scan dir     where the e2e fixture tree is generated and scanned
#
# Each host's platform is detected with uname, and the binary and tests are
# built once per platform. A failing host is reported and the others go on.
#
#   deploy       upload the binary (checksum-verified, atomic rename) and scripts
#   smoke        deploy, then run `uvsh version`
#   fixture      rebuild the e2e fixture in the scan dir
#   e2e          deploy, rebuild the fixture, run e2e.sh against it
#   test-remote  run the unit tests (no -race: it needs cgo)
#   bench        deploy, then bench.sh on BENCH_DIR [BENCH_EXCLUDES] [BENCH_RUNS]
set -eu

die() {
	echo "$*" >&2
	exit 2
}

[ $# -eq 1 ] || die "usage: $0 deploy|smoke|fixture|e2e|test-remote|bench"
action=$1
case $action in
deploy | smoke | fixture | e2e | test-remote | bench) ;;
*) die "unknown action: $action" ;;
esac
[ -n "${DEPLOY_HOST:-}" ] || die "DEPLOY_HOST is not set; see local.mk.example"
[ "$action" != bench ] || [ -n "${BENCH_DIR:-}" ] || die "BENCH_DIR is not set; see local.mk.example"

# BatchMode: fail instead of prompting for a password; the timeout bounds dead hosts.
SSH="ssh -o BatchMode=yes -o ConnectTimeout=10"

build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT
failed=0

fail() {
	echo "FAIL $host: $*"
	failed=$((failed + 1))
}

# detect: set goos/goarch/goarm from the host's `uname -sm`.
detect() {
	plat=
	plat=$($SSH "$host" uname -sm 2>/dev/null) || return 1
	case $plat in
	"Linux x86_64") goos=linux goarch=amd64 goarm= ;;
	"Linux aarch64") goos=linux goarch=arm64 goarm= ;;
	"Linux armv7l") goos=linux goarch=arm goarm=7 ;;
	"Linux armv6l") goos=linux goarch=arm goarm=6 ;;
	"Linux i686" | "Linux i386") goos=linux goarch=386 goarm= ;;
	"Darwin arm64") goos=darwin goarch=arm64 goarm= ;;
	"Darwin x86_64") goos=darwin goarch=amd64 goarm= ;;
	"FreeBSD amd64") goos=freebsd goarch=amd64 goarm= ;;
	*) return 1 ;;
	esac
	out=$build/$goos-$goarch$goarm
}

gobuild() {
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch GOARM=$goarm "$@"
}

upload_scripts() {
	tar -C scripts -c mkfixture.sh e2e.sh bench.sh |
		$SSH "$host" "mkdir -p '$runtime' && tar -x -C '$runtime'"
}

# deploy: write to a temp name, verify the checksum, then rename. rename(2) is
# atomic, so a running uvsh never sees a half-written binary.
deploy() {
	[ -f "$out/uvsh" ] || gobuild go build -trimpath -o "$out/uvsh" ./cmd/uvsh || return 1
	$SSH "$host" "mkdir -p '$runtime' && cat > '$runtime/uvsh.new'" <"$out/uvsh" || return 1
	want=$(sha256sum "$out/uvsh" | cut -d' ' -f1)
	got=$($SSH "$host" "sha256sum '$runtime/uvsh.new'" | cut -d' ' -f1) || return 1
	[ "$want" = "$got" ] || { echo "checksum mismatch: local=$want remote=$got"; return 1; }
	$SSH "$host" "chmod 755 '$runtime/uvsh.new' && mv -f '$runtime/uvsh.new' '$runtime/uvsh'" || return 1
	upload_scripts
}

# fixture: -clean only removes a dir carrying the fixture marker, so a wrong
# scan dir fails instead of deleting data.
fixture() {
	upload_scripts || return 1
	$SSH "$host" "[ ! -e '$scan' ] || sh '$runtime/mkfixture.sh' -clean '$scan'; sh '$runtime/mkfixture.sh' '$scan'"
}

test_remote() {
	[ -d "$out/test" ] || gobuild go test -c -o "$out/test/" ./... >/dev/null || return 1
	t=$runtime/test
	tar -C "$out/test" -c . | $SSH "$host" "rm -rf '$t' && mkdir -p '$t' && tar -x -C '$t'" || return 1
	$SSH "$host" "cd '$t' && rc=0
		for f in *.test; do
			printf '   %-14s ' \"\$f\"
			if ./\"\$f\" >out 2>&1; then echo ok; else echo FAIL; cat out; rc=1; fi
		done
		cd / && rm -rf '$t'
		exit \$rc"
}

run() {
	case $action in
	deploy) deploy ;;
	smoke) deploy && $SSH "$host" "'$runtime/uvsh' version" ;;
	fixture) fixture ;;
	e2e) deploy && fixture >/dev/null && $SSH "$host" "sh '$runtime/e2e.sh' '$runtime/uvsh' '$scan'" ;;
	test-remote) test_remote ;;
	bench) deploy && $SSH "$host" "sh '$runtime/bench.sh' '$runtime/uvsh' '$BENCH_DIR' '${BENCH_EXCLUDES:-}' '${BENCH_RUNS:-3}'" ;;
	esac
}

set -f # entries are split on commas, never globbed
ifs=$IFS
IFS=,
for entry in $DEPLOY_HOST; do
	IFS=$ifs
	entry=$(printf '%s' "$entry" | tr -d ' \t')
	[ -n "$entry" ] || continue
	host=$entry
	case $entry in
	*:*:*:*) fail "more than two ':' (want user@host:/runtime/dir:/scan/dir)"; continue ;;
	?*:/*:/?*) ;;
	*) fail "want user@host:/runtime/dir:/scan/dir"; continue ;;
	esac
	host=${entry%%:*}
	rest=${entry#*:}
	runtime=${rest%%:*}
	scan=${rest#*:}

	if ! detect; then
		fail "unreachable or unsupported platform${plat:+ \"$plat\"}"
		continue
	fi
	echo "== $host ($goos/$goarch${goarm:+/v$goarm}) runtime=$runtime scan=$scan"
	run || fail "$action failed"
done
IFS=$ifs

[ "$failed" -eq 0 ] || { echo "$failed host(s) failed" >&2; exit 1; }
echo "all hosts passed"
