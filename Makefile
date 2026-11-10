SHELL := /bin/sh
.DEFAULT_GOAL := check

VERSION ?= 0.0.0-dev
PROBE_RUN_ID ?= validation
GO_DIRS := cmd internal tests probes

# Keep build, dependency, and temporary files inside this checkout.
export GOTOOLCHAIN := local
export GOCACHE := $(CURDIR)/.cache/go-build
export GOMODCACHE := $(CURDIR)/.cache/go-mod
export GOPATH := $(CURDIR)/.cache/gopath
export GOTMPDIR := $(CURDIR)/.cache/tmp
export TMPDIR := $(CURDIR)/.cache/tmp
export GOFLAGS := -mod=readonly
export CGO_ENABLED := 0

.PHONY: prepare deps build fmt fmt-check vet test test-unit test-integration test-e2e check verify-probes

prepare:
	@mkdir -p "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)"

deps: prepare
	go mod download
	go mod verify

build: prepare
	@mkdir -p bin
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/agentflow ./cmd/agentflow

fmt:
	gofmt -w $(GO_DIRS)

fmt-check:
	@files="$$(gofmt -l $(GO_DIRS))" && test -z "$$files" || { gofmt -l $(GO_DIRS); exit 1; }

vet: prepare
	go vet ./...

test: prepare
	go test ./...

test-unit: prepare
	go test ./internal/...

test-integration: prepare
	go test ./tests/integration/...

test-e2e: prepare
	go test ./tests/e2e/...

check: fmt-check deps vet test build

# Records real S1 commands; refuses to overwrite an existing validation run.
verify-probes: prepare
	go run ./probes/verify --run-id "$(PROBE_RUN_ID)"
