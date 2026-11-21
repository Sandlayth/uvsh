# Developer recipes for the host machine. Cross-compiling and deploying to
# another device live in cross.mk (run with `make -f cross.mk <target>`).
# Personal settings go in an untracked local.mk; see local.mk.example.
-include local.mk

BIN := bin

GOFLAGS_BUILD := -trimpath

BENCH_DIR      ?=
BENCH_EXCLUDES ?=
BENCH_RUNS     ?= 3
BENCH_DB       ?=

.PHONY: build test vet fmt-check nocgo check e2e bench clean

build:
	go build $(GOFLAGS_BUILD) -o $(BIN)/uvsh ./cmd/uvsh

test:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	@out=$$(gofmt -l .); test -z "$$out" || { echo "gofmt needed on:"; echo "$$out"; exit 1; }

# Release builds must not depend on C. The race detector needs cgo, so `test`
# can't prove that; this build can.
nocgo:
	CGO_ENABLED=0 go build -o /dev/null ./...

# Everything that must pass before a commit.
check: fmt-check vet test nocgo

# Build the fixture tree in a temp dir, check uvsh against it, clean up.
e2e: build
	@dir=$$(mktemp -d) && trap 'scripts/mkfixture.sh -clean "$$dir/fx"; rmdir "$$dir"' EXIT && \
	scripts/mkfixture.sh "$$dir/fx" >/dev/null && scripts/e2e.sh $(BIN)/uvsh "$$dir/fx"

# Time, peak memory and disk writes of a real scan: make bench BENCH_DIR=/some/dir
bench: build
	@test -n "$(BENCH_DIR)" || { echo "set BENCH_DIR=/dir/to/scan (and optionally BENCH_EXCLUDES, BENCH_RUNS, BENCH_DB)"; exit 1; }
	@scripts/bench.sh $(BIN)/uvsh "$(BENCH_DIR)" "$(BENCH_EXCLUDES)" $(BENCH_RUNS) "$(BENCH_DB)"

clean:
	rm -rf $(BIN)
