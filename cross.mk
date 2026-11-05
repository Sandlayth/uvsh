# Cross-compile uvsh and deploy it to a remote machine over ssh.
#
#   make -f cross.mk cross    # build for TARGET_* into bin/
#   make -f cross.mk smoke    # build, deploy to DEPLOY_HOST, run `uvsh version`
#
# Machine-specific settings live in an untracked local.mk, e.g.:
#
#   DEPLOY_HOST = user@host
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
.PHONY: cross deploy smoke

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
