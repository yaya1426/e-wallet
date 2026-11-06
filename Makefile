# Developer and CI entry points. `make help` lists the targets.

GOLANGCI_LINT_VERSION := v2.15.0
MIGRATE_VERSION       := v4.20.1
SQLC_VERSION          := v1.31.1

BIN           := $(CURDIR)/bin
GOLANGCI_LINT := $(BIN)/golangci-lint
MIGRATE       := $(BIN)/migrate
SQLC          := $(BIN)/sqlc
COMPOSE       := docker compose -f docker-compose.yml

POSTGRES_HOST_PORT ?= 5432
LOCAL_DATABASE_URL ?= postgres://wallet:wallet@localhost:$(POSTGRES_HOST_PORT)/wallet?sslmode=disable

.DEFAULT_GOAL := help
.PHONY: help tools tool-sqlc fmt fmt-check tidy-check vet lint sqlc sqlc-check build test test-integration cover \
        up down migrate migrate-down migration e2e ci

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-18s %s\n", $$1, $$2}'

tools: tool-sqlc ## Install golangci-lint, the migrate CLI and sqlc into ./bin
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOBIN=$(BIN) go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

tool-sqlc: ## Install the pinned sqlc into ./bin (also used by CI)
	GOBIN=$(BIN) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

fmt: ## Format all Go files
	gofmt -w .

fmt-check: ## Fail if any Go file is not gofmt-formatted
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tidy-check: ## Fail if go.mod/go.sum are not tidy
	go mod tidy -diff

vet: ## Run go vet (all build tags)
	go vet -tags 'integration e2e' ./...

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

sqlc: ## Generate Go code from the modules' queries.sql (sqlc.yaml)
	$(SQLC) generate

sqlc-check: ## Fail if the generated sqlc code is not up to date
	$(SQLC) generate
	@changed="$$(git diff --name-only -- 'internal/*/internal/sqlcgen/*')"; \
	untracked="$$(git ls-files --others --exclude-standard -- 'internal/*/internal/sqlcgen/*')"; \
	if [ -n "$$changed$$untracked" ]; then echo "sqlc code is stale; run 'make sqlc' and commit:"; echo "$$changed $$untracked"; exit 1; fi

build: ## Build all packages and binaries
	go build ./...

test: ## Unit tests with the race detector
	go test -race -count=1 -cover ./...

test-integration: ## Integration tests (needs Docker for testcontainers)
	go test -race -count=1 -tags integration ./...

cover: ## Coverage of unit + integration tests per package
	go test -count=1 -tags integration -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

up: ## Build and start the local stack (postgres, migrate, wallet, banksim)
	$(COMPOSE) up -d --build --wait

down: ## Stop the local stack and remove its volumes
	$(COMPOSE) down -v

migrate: ## Apply migrations to the compose database (wallet migrate up)
	$(COMPOSE) build wallet
	$(COMPOSE) run --rm migrate

migrate-down: ## Roll back the last migration locally (migrate CLI, local only)
	$(MIGRATE) -path migrations -database '$(LOCAL_DATABASE_URL)' down 1

migration: ## Create a new migration pair: make migration name=<snake_name>
	@test -n "$(name)" || (echo "usage: make migration name=<snake_name>"; exit 1)
	$(MIGRATE) create -ext sql -dir migrations -seq -digits 6 $(name)

e2e: ## Smoke tests against the running compose stack
	go test -count=1 -tags e2e ./test/e2e/...

ci: tidy-check fmt-check vet lint sqlc-check build test test-integration ## Every CI check except the compose smoke test
