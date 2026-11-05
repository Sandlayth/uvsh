# Developer recipes for the host machine. Cross-compiling and deploying to
# another device live in cross.mk (run with `make -f cross.mk <target>`).

BIN := bin

GOFLAGS_BUILD := -trimpath

.PHONY: build test vet fmt-check nocgo check clean

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

clean:
	rm -rf $(BIN)
