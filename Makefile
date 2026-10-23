GO      ?= go
BIN_DIR ?= bin

# Load .env when present so `make run-*` picks up local overrides.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help run-api run-worker migrate build test test-integration test-race fmt vet clean

help: ## List available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-12s %s\n", $$1, $$2}'

run-api: ## Run the API locally
	$(GO) run ./cmd/api

run-worker: ## Run the background worker locally
	$(GO) run ./cmd/worker

migrate: ## Apply User module DB migrations (needs USER_MIGRATE_DATABASE_URL)
	$(GO) run ./cmd/migrate up

build: ## Build binaries into ./bin
	$(GO) build -o $(BIN_DIR)/api ./cmd/api
	$(GO) build -o $(BIN_DIR)/worker ./cmd/worker

test: ## Run unit tests
	$(GO) test ./...

test-integration: ## Run DB integration tests (needs USER_IT_SUPERUSER_URL)
	$(GO) test -count=1 ./internal/user/...

test-race: ## Run unit tests with the race detector
	$(GO) test -race ./...

fmt: ## Format Go code
	$(GO) fmt ./...

vet: ## Run static checks
	$(GO) vet ./...

clean: ## Remove build output
	rm -rf $(BIN_DIR)
