# Project setup

> What to install, how to bootstrap a fresh checkout, what `make` targets do what.

## Prerequisites

Versions pinned in `tools.go` and `Makefile`:

- **Go 1.22+** (we use `log/slog`, `errors.Join`, generics).
- **Docker** for local dev (Postgres, LocalStack).
- **Docker Compose** v2.
- **`golangci-lint`** (installed via `make tools`).
- **`sqlc`** (installed via `make tools`).
- **`golang-migrate`** (installed via `make tools`).
- **`mockgen`** — *not used*; we hand-write fakes.

## First-time setup

```bash
git clone git@github.com:pikshipp/pikshipp.git
cd pikshipp
make tools          # installs sqlc, migrate, golangci-lint
make deps           # go mod download
make dev-up         # docker compose up postgres localstack -d
make migrate-up     # run migrations against local DB
make seed           # optional: seed local with test data
make run            # run binary in --role=all mode
```

After `make run`, the API listens on `localhost:8080`. Health check: `curl localhost:8080/healthz`.

## go.mod

Module name: `github.com/pikshipp/pikshipp`.

Pinned dependency versions managed via `go.sum`. Upgrades go through PR with explicit reasoning.

## Makefile

```makefile
.PHONY: tools deps lint test slt bench bench-compare run dev-up dev-down migrate-up migrate-down sqlc-gen ci

tools:
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.59.1

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

bench-compare:
	go test ./internal/... -bench=. -benchmem -run=^$$ -count=10 -benchtime=3s > new.bench
	benchstat main.bench new.bench

run:
	go run ./cmd/pikshipp --role=all

dev-up:
	docker compose -f deploy/dev/docker-compose.yml up -d postgres localstack

dev-down:
	docker compose -f deploy/dev/docker-compose.yml down

migrate-up:
	migrate -path migrations -database "$$DATABASE_URL" up

migrate-down:
	migrate -path migrations -database "$$DATABASE_URL" down 1

sqlc-gen:
	sqlc generate

ci: lint test slt bench
```

## docker-compose.yml (dev)

```yaml
version: '3.8'

services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: pikshipp_dev
      POSTGRES_PASSWORD: dev
      POSTGRES_DB: pikshipp_dev
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
    command: >
      postgres
      -c shared_preload_libraries=pg_stat_statements
      -c log_min_duration_statement=100

  localstack:
    image: localstack/localstack:latest
    environment:
      SERVICES: s3,ses
      DEFAULT_REGION: ap-south-1
    ports:
      - "4566:4566"
    volumes:
      - localstack_data:/var/lib/localstack

volumes:
  postgres_data:
  localstack_data:
```

## .env (dev)

```bash
# .env (NOT committed; .gitignore'd)
PIKSHIPP_ROLE=all
PIKSHIPP_DATABASE_URL=postgres://pikshipp_dev:dev@localhost:5432/pikshipp_dev?sslmode=disable
PIKSHIPP_S3_REGION=ap-south-1
PIKSHIPP_S3_ENDPOINT=http://localhost:4566   # localstack
PIKSHIPP_S3_BUCKET=pikshipp-dev
PIKSHIPP_S3_FORCE_PATH_STYLE=true            # required for localstack

PIKSHIPP_GOOGLE_OAUTH_CLIENT_ID=dev-client-id
PIKSHIPP_GOOGLE_OAUTH_CLIENT_SECRET=dev-secret
PIKSHIPP_GOOGLE_OAUTH_REDIRECT_URL=http://localhost:8080/v1/auth/google/callback

PIKSHIPP_SESSION_HMAC_KEY=dev-256-bit-hmac-key-replace-in-prod-please-do-not-use

PIKSHIPP_RAZORPAY_KEY_ID=rzp_test_dev
PIKSHIPP_RAZORPAY_KEY_SECRET=rzp_secret_dev

PIKSHIPP_DELHIVERY_API_KEY=dev-stub
PIKSHIPP_DELHIVERY_BASE_URL=https://staging-express.delhivery.com

PIKSHIPP_MSG91_AUTH_KEY=dev-stub
PIKSHIPP_SES_FROM_EMAIL=noreply@dev.pikshipp.local

PIKSHIPP_LOG_LEVEL=debug
PIKSHIPP_VERSION=dev
```

A `.env.example` IS committed.

## .golangci.yml

```yaml
run:
  timeout: 5m
  go: '1.22'

linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - typecheck
    - unused
    - bodyclose
    - sqlclosecheck
    - gosec
    - revive
    - nilerr
    - rowserrcheck
    - depguard
    - gocyclo
    - misspell
    - unconvert
    - dupl

linters-settings:
  gocyclo:
    min-complexity: 15

  depguard:
    rules:
      domain:
        list-mode: lax
        files:
          - "internal/wallet/**"
          - "internal/orders/**"
          - "internal/shipments/**"
          # ... all domain modules
        deny:
          - pkg: "github.com/aws/**"
            desc: "Domain modules must not import vendor SDKs directly. Use adapters."
          - pkg: "net/http"
            desc: "Domain modules must not make HTTP calls. Use injected interfaces."

  revive:
    rules:
      - name: blank-imports
      - name: context-as-argument
      - name: dot-imports
      - name: error-return
      - name: error-naming
      - name: var-naming

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - gocyclo
        - gosec
```

## .github/workflows/ci.yml

```yaml
name: CI
on:
  pull_request:
  push:
    branches: [main]

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: make tools
      - run: make lint

  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16-alpine
        env:
          POSTGRES_PASSWORD: ci
        ports: ['5432:5432']
        options: >-
          --health-cmd pg_isready --health-interval 5s --health-retries 5
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: make tools
      - run: make migrate-up
        env:
          DATABASE_URL: postgres://postgres:ci@localhost:5432/postgres?sslmode=disable
      - run: make test
      - run: make slt

  bench:
    runs-on: ubuntu-latest
    needs: test
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: make bench
```

## Local DB management

```bash
make migrate-up                  # apply pending migrations
make migrate-down                # roll back one migration
make sqlc-gen                    # regenerate Go from .sql queries
psql $DATABASE_URL               # interactive shell
```

## Editor setup

VS Code recommended; `.vscode/settings.json` (committed):

```json
{
  "go.lintTool": "golangci-lint",
  "go.formatTool": "gofumpt",
  "[go]": {
    "editor.formatOnSave": true,
    "editor.codeActionsOnSave": {
      "source.organizeImports": "explicit"
    }
  },
  "go.testEnvFile": "${workspaceFolder}/.env"
}
```

Recommended extensions in `.vscode/extensions.json`.

## Versioning

- Application version: git SHA at build time.
- Schema version: tracked in `schema_migrations` table by golang-migrate.
- API version: `/v1/...` URL path; bumps documented in CHANGELOG.

## Ground rules for first-day developer

1. Read `00-conventions/01-go-conventions.md`. All of it.
2. `make tools && make dev-up && make migrate-up && make run`.
3. Pick a section in `03-services/` you're assigned. Implement against the LLD.
4. Open a PR. Reviewer checks: tests, lint, godoc, conventions adherence.
