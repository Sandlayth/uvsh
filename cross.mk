# Cross-compile uvsh, and run development actions on remote machines.
#
#   make -f cross.mk cross        # build for TARGET_* into bin/ (no host involved)
#   make -f cross.mk deploy       # upload the binary to every host in DEPLOY_HOST
#   make -f cross.mk smoke        # deploy, run `uvsh version`
#   make -f cross.mk fixture      # (re)build the e2e fixture in each scan dir
#   make -f cross.mk e2e          # deploy, rebuild the fixture, check uvsh against it
#   make -f cross.mk test-remote  # run the unit tests on each host
#   make -f cross.mk bench        # time and peak memory of a scan of BENCH_DIR
#
# Settings come from an untracked local.mk; see local.mk.example.
-include local.mk

BIN := bin

# The default is 32-bit ARMv7, the smallest platform UVSH supports
TARGET_GOOS   ?= linux
TARGET_GOARCH ?= arm
TARGET_GOARM  ?= 7
TARGET_BIN    := $(BIN)/uvsh-$(TARGET_GOOS)-$(TARGET_GOARCH)

BENCH_RUNS ?= 3

GOFLAGS_BUILD := -trimpath

REMOTE_ACTIONS := deploy smoke fixture e2e test-remote bench

.DEFAULT_GOAL := cross
.PHONY: cross $(REMOTE_ACTIONS)

cross:
	CGO_ENABLED=0 GOOS=$(TARGET_GOOS) GOARCH=$(TARGET_GOARCH) GOARM=$(TARGET_GOARM) \
		go build $(GOFLAGS_BUILD) -o $(TARGET_BIN) ./cmd/uvsh

# Each host's platform is detected, so TARGET_* doesn't apply here.
$(REMOTE_ACTIONS):
	@DEPLOY_HOST='$(DEPLOY_HOST)' BENCH_DIR='$(BENCH_DIR)' BENCH_EXCLUDES='$(BENCH_EXCLUDES)' \
		BENCH_RUNS='$(BENCH_RUNS)' scripts/remote.sh $@
