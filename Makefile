GO      ?= go
BIN_DIR ?= bin

# Load .env when present so `make run-*` picks up local overrides.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help run-api run-worker build test test-race test-integration migrate migrate-version fmt vet clean

help: ## List available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-12s %s\n", $$1, $$2}'

run-api: ## Run the API locally
	$(GO) run ./cmd/api

run-worker: ## Run the background worker locally
	$(GO) run ./cmd/worker

build: ## Build binaries into ./bin
	$(GO) build -o $(BIN_DIR)/api ./cmd/api
	$(GO) build -o $(BIN_DIR)/worker ./cmd/worker
	$(GO) build -o $(BIN_DIR)/migrate ./cmd/migrate

migrate: ## Apply forward-only migrations (needs USER_MIGRATE_DATABASE_URL)
	$(GO) run ./cmd/migrate up

migrate-version: ## Print the current migration version (needs USER_MIGRATE_DATABASE_URL)
	$(GO) run ./cmd/migrate version

test: ## Run unit tests
	$(GO) test ./...

test-race: ## Run unit tests with the race detector
	$(GO) test -race ./...

test-integration: ## Run DB integration tests (needs Postgres via FMT8_TEST_ADMIN_URL)
	$(GO) test -tags integration ./...

fmt: ## Format Go code
	$(GO) fmt ./...

vet: ## Run static checks
	$(GO) vet ./...

clean: ## Remove build output
	rm -rf $(BIN_DIR)
