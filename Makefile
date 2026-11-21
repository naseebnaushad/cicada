# Build label comes from scripts/version.sh; it needs a VERSION file, so fall back until
# the first version is opened with /promote.
VERSION ?= $(shell ./scripts/version.sh label 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
PKG     := github.com/naseebnaushad/cicada/internal/platform/buildinfo
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT)

.PHONY: build test lint vuln cross

build: ## Build bin/cicada for this machine
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cicada ./cmd/cicada

test: ## Run unit tests with the race detector
	go test -race ./...

lint: ## Format check, vet and staticcheck
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	staticcheck ./...

vuln: ## Known-vulnerability scan
	govulncheck ./...

cross: ## Prove the binary builds for every supported OS
	@for t in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64; do \
		echo "build $$t"; \
		GOOS=$${t%/*} GOARCH=$${t#*/} CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o /dev/null ./cmd/cicada || exit 1; \
	done
