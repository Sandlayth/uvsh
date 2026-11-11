# Cross-compile uvsh and deploy it to a remote machine over ssh.
#
#   make -f cross.mk cross    # build for TARGET_* into bin/
#   make -f cross.mk smoke    # build, deploy to DEPLOY_HOST, run `uvsh version`
#   make -f cross.mk fixture  # (re)build the e2e test tree in E2E_DIR on DEPLOY_HOST
#
# Machine-specific settings live in an untracked local.mk, e.g.:
#
#   DEPLOY_HOST = user@host
#   E2E_DIR = /path/on/host/uvsh-e2e
#   TARGET_GOARCH = arm64
#
-include local.mk

BIN := bin

# The default is 32-bit ARMv7, the smallest platform UVSH supports
TARGET_GOOS   ?= linux
TARGET_GOARCH ?= arm
TARGET_GOARM  ?= 7
TARGET_BIN    := $(BIN)/uvsh-$(TARGET_GOOS)-$(TARGET_GOARCH)

DEPLOY_DIR ?= /opt/uvsh

GOFLAGS_BUILD := -trimpath

.DEFAULT_GOAL := cross
.PHONY: cross deploy smoke fixture

cross:
	CGO_ENABLED=0 GOOS=$(TARGET_GOOS) GOARCH=$(TARGET_GOARCH) GOARM=$(TARGET_GOARM) \
		go build $(GOFLAGS_BUILD) -o $(TARGET_BIN) ./cmd/uvsh

# Upload via plain ssh (no scp/sftp dependency), write to a temp name, verify
# the checksum, then rename. rename(2) is atomic, so a running uvsh never sees
# a half-written binary and we avoid "text file busy" errors.
deploy: cross
	@test -n "$(DEPLOY_HOST)" || { echo "DEPLOY_HOST is not set; put 'DEPLOY_HOST = user@host' in local.mk"; exit 1; }
	ssh $(DEPLOY_HOST) 'mkdir -p $(DEPLOY_DIR) && cat > $(DEPLOY_DIR)/uvsh.new' < $(TARGET_BIN)
	@want=$$(sha256sum $(TARGET_BIN) | cut -d' ' -f1); \
	got=$$(ssh $(DEPLOY_HOST) 'sha256sum $(DEPLOY_DIR)/uvsh.new' | cut -d' ' -f1); \
	test "$$want" = "$$got" || { echo "checksum mismatch: local=$$want remote=$$got"; exit 1; }
	ssh $(DEPLOY_HOST) 'chmod 755 $(DEPLOY_DIR)/uvsh.new && mv -f $(DEPLOY_DIR)/uvsh.new $(DEPLOY_DIR)/uvsh'
	@echo "deployed to $(DEPLOY_HOST):$(DEPLOY_DIR)/uvsh"

smoke: deploy
	ssh $(DEPLOY_HOST) '$(DEPLOY_DIR)/uvsh version'

# Rebuilds the tree from scratch each time. -clean only removes a directory
# carrying the fixture marker, so a wrong E2E_DIR fails instead of deleting data.
fixture:
	@test -n "$(DEPLOY_HOST)" || { echo "DEPLOY_HOST is not set; see local.mk"; exit 1; }
	@test -n "$(E2E_DIR)" || { echo "E2E_DIR is not set; see local.mk"; exit 1; }
	ssh $(DEPLOY_HOST) 'mkdir -p $(DEPLOY_DIR) && cat > $(DEPLOY_DIR)/mkfixture.sh' < scripts/mkfixture.sh
	ssh $(DEPLOY_HOST) '[ ! -e $(E2E_DIR) ] || sh $(DEPLOY_DIR)/mkfixture.sh -clean $(E2E_DIR); sh $(DEPLOY_DIR)/mkfixture.sh $(E2E_DIR)'
