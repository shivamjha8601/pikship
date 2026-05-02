.PHONY: tools deps lint test slt bench run dev-up dev-down \
        db-bootstrap db-grant-roles dev-setup \
        migrate-up migrate-down migrate-version migrate-force \
        sqlc-gen ci

# If a local .env exists, load it so `make run` and friends can read
# PIKSHIPP_DATABASE_URL et al without manual exports. .env is .gitignore'd.
ifneq (,$(wildcard .env))
include .env
export
endif

# Local dev defaults (overridable via env). The repo's docker postgres is
# expected to be running and reachable on 127.0.0.1:5432.
export PIKSHIPP_DB_CONTAINER ?= postgres-db
export PIKSHIPP_DB_SUPERUSER ?= root
export PIKSHIPP_DB_SUPERPASS ?= root
export PIKSHIPP_DB_HOST      ?= 127.0.0.1
export PIKSHIPP_DB_PORT      ?= 5432
export PIKSHIPP_DB_NAME      ?= pikshipp_dev
export PIKSHIPP_APP_USER     ?= pikshipp_dev
export PIKSHIPP_APP_PASS     ?= dev

# Migrations connect as the superuser to create roles, RLS policies, etc.
MIGRATE_URL ?= postgres://$(PIKSHIPP_DB_SUPERUSER):$(PIKSHIPP_DB_SUPERPASS)@$(PIKSHIPP_DB_HOST):$(PIKSHIPP_DB_PORT)/$(PIKSHIPP_DB_NAME)?sslmode=disable

# Runtime app connects as a login user; SET ROLE happens in pool AfterConnect.
DATABASE_URL ?= postgres://$(PIKSHIPP_APP_USER):$(PIKSHIPP_APP_PASS)@$(PIKSHIPP_DB_HOST):$(PIKSHIPP_DB_PORT)/$(PIKSHIPP_DB_NAME)?sslmode=disable

tools:
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0

deps:
	go mod download

lint:
	golangci-lint run --timeout=5m

test:
	go test ./internal/... ./api/... -count=1 -race -short

slt:
	go test ./internal/... -count=1 -race -tags=slt

bench:
	go test ./internal/... -bench=. -benchmem -run=^$$ -benchtime=3s

run:
	go run ./cmd/pikshipp --role=all

dev-up:
	docker compose -f deploy/dev/docker-compose.yml up -d postgres

dev-down:
	docker compose -f deploy/dev/docker-compose.yml down

# db-bootstrap creates the dev database and the login user.
db-bootstrap:
	./scripts/db-bootstrap.sh

# db-grant-roles grants the pikshipp_* roles (created by migration 0001)
# to the login user. Run after migrate-up.
db-grant-roles:
	./scripts/db-grant-roles.sh

# dev-setup is the one-shot for a fresh checkout against the running
# docker postgres: create db + login user, run migrations, grant roles.
dev-setup: db-bootstrap migrate-up db-grant-roles
	@echo "[dev-setup] ready. DATABASE_URL=$(DATABASE_URL)"

migrate-up:
	migrate -path migrations -database "$(MIGRATE_URL)" up

migrate-down:
	migrate -path migrations -database "$(MIGRATE_URL)" down 1

migrate-version:
	migrate -path migrations -database "$(MIGRATE_URL)" version

migrate-force:
	@if [ -z "$(V)" ]; then echo "Usage: make migrate-force V=<version>"; exit 1; fi
	migrate -path migrations -database "$(MIGRATE_URL)" force $(V)

sqlc-gen:
	sqlc generate

ci: lint test slt
