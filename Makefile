# Makefile for m3u8dl
# Targets referenced by CONTRIBUTING.md / GitHub Actions / .goreleaser.yml

SHELL := /bin/bash

APP_NAME := m3u8dl
BIN_DIR  := bin
GO       ?= go
PACKAGE  := github.com/dingdayu/m3u8dl

# ---- Version support -------------------------------------------------------

# Reads the semantic version from the most recent git tag (e.g. v1.2.3 -> 1.2.3),
# falling back to "0.0.0-dev" when there are no tags yet.
# The running m3u8dl binary can also be built/run as a scripted alternative:
#   make build APP_VERSION=1.2.3
APP_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')
ifeq ($(APP_VERSION),)
APP_VERSION := 0.0.0-dev
endif
VERSION ?= $(APP_VERSION)

# Commit SHA and build date for the version string.
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X "main.version=$(VERSION)" \
	-X "main.commit=$(GIT_COMMIT)" \
	-X "main.date=$(BUILD_DATE)"

# ---- Targets ---------------------------------------------------------------

.PHONY: build
build: fmt-check vet ## gofmt check + vet + build to ./bin/m3u8dl
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP_NAME) .

.PHONY: test
test: ## run the test suite with race detector
	$(GO) test ./... -race -count=1

.PHONY: vet
vet: ## run go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## format the code
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## fail if any file is not gofmt-formatted
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "The following files are not gofmt formatted:"; \
		echo "$$files"; \
		exit 1; \
	fi

.PHONY: lint
lint: ## run golangci-lint (if installed)
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Install: https://golangci-lint.run"; exit 1; }; \
	golangci-lint run

.PHONY: release
release: ## run goreleaser (snapshot mode unless GIT_TAG set). Requires goreleaser.
	@command -v goreleaser >/dev/null 2>&1 || { \
		echo "goreleaser not found. Install: https://goreleaser.com/install"; exit 1; }; \
	goreleaser release --clean

.PHONY: release-snapshot
release-snapshot: ## build a local snapshot with goreleaser (no publish)
	@command -v goreleaser >/dev/null 2>&1 || { \
		echo "goreleaser not found. Install: https://goreleaser.com/install"; exit 1; }; \
	goreleaser release --snapshot --clean

.PHONY: app-version
app-version: ## print the current application version (used by release helpers)
	@echo $(VERSION)

.PHONY: install-hooks
install-hooks: ## install git hooks (core.hooksPath -> .hooks)
	@chmod +x .hooks/pre-commit .hooks/commit-msg .hooks/install.sh
	git config core.hooksPath .hooks
	@echo "Hooks installed. Active: pre-commit (gofmt/vet/style), commit-msg (conventional commits)."

.PHONY: style
style: ## normalize text files (LF + EOF newline + no trailing whitespace)
	@chmod +x scripts/normalize-format.sh
	@scripts/normalize-format.sh

.PHONY: style-check
style-check: ## check only; exit 1 if any file is not normalized
	@chmod +x scripts/normalize-format.sh
	@scripts/normalize-format.sh --check
	@echo "Style normalization done. Review changes with: git diff --stat"

.PHONY: pre-commit
pre-commit: ## run the pre-commit framework hooks across the repo (if installed)
	@command -v pre-commit >/dev/null 2>&1 || { \
		echo "pre-commit not found. Install: pip install pre-commit"; exit 1; }; \
	pre-commit run --all-files

.PHONY: commit-msg-check
commit-msg-check: ## check the latest commit message against Conventional Commits
	@.hooks/commit-msg <(git log -1 --format=%s) ; echo "exit code: $$?" || true

.PHONY: clean
clean: ## remove build artifacts
	rm -rf $(BIN_DIR) dist

.PHONY: help
help: ## show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
