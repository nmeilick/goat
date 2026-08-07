.DEFAULT_GOAL := _default

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/nmeilick/goat/cmd.Version=$(VERSION) \
	-X github.com/nmeilick/goat/cmd.Commit=$(COMMIT) \
	-X github.com/nmeilick/goat/cmd.Date=$(BUILD_DATE)

.PHONY: _default help build test lint fmt fmt-check check smoke clean

_default:
	@echo "hint: run 'make help' to list available targets"
	@$(MAKE) --no-print-directory build

help: ## List available targets
	@echo "goat development tasks:"
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build bin/goat with version metadata
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/goat .

test: ## Run all tests
	go test ./...

lint: ## Run go vet
	go vet ./...

fmt: ## Format all Go files
	gofmt -w .

fmt-check: ## Verify all Go files are gofmt-clean
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then echo "needs gofmt:"; echo "$$files"; exit 1; fi

check: fmt-check lint test ## Run fmt-check, lint and test

smoke: build ## Build and run bin/goat version
	./bin/goat version

clean: ## Remove build artifacts
	rm -rf bin
