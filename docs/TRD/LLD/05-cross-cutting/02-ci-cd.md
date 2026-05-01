# CI / CD

## Purpose

Pikshipp ships from a single Go monolith repo. CI must be:

- **Fast** — PR-to-merge feedback under 10 minutes.
- **Deterministic** — flaky tests are blocked from merging.
- **Honest** — what runs in CI is identical to what runs in prod.

We use **GitHub Actions** for CI and a deploy tool (`make deploy` invoking `cdk deploy` or `kubectl apply`) for CD. Migrations run as a CI step **before** the new binary boots, per ADR 0009.

## Pipeline Stages

```
Push / PR ─►  lint ─►  unit ─►  slt ─►  race ─►  build ─►  package ─►  deploy-staging ─►  smoke ─►  deploy-prod (manual)
```

Each stage is a separate GitHub Actions job; later stages depend on earlier ones via `needs:`.

## Stage 1: Lint

```yaml
name: lint
on: [pull_request, push]
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22.x', cache: true }
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v4
        with: { version: v1.55.x }
      - name: go vet
        run: go vet ./...
      - name: gofumpt check
        run: |
          go install mvdan.cc/gofumpt@v0.5.0
          if [ -n "$(gofumpt -l .)" ]; then
            echo "::error::Run gofumpt -w ."
            gofumpt -l .
            exit 1
          fi
```

The `.golangci.yml` enables: `errcheck`, `staticcheck`, `gosec`, `bodyclose`, `goimports`, `revive` (subset), `unused`, `gosimple`, `misspell`, `gocritic` (a curated subset). We **don't** enable `wsl`, `nlreturn`, or anything that argues about whitespace.

## Stage 2: Unit Tests

```yaml
unit:
  needs: lint
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version: '1.22.x', cache: true }
    - name: go test
      run: go test -count=1 -timeout 5m ./...
```

`-count=1` defeats the test cache so reruns are honest. Unit tests must finish in < 2 minutes.

## Stage 3: SLT (System-Level Tests)

```yaml
slt:
  needs: unit
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version: '1.22.x', cache: true }
    - name: docker buildx setup
      uses: docker/setup-buildx-action@v3
    - name: SLT
      run: go test -tags slt -count=1 -timeout 15m -p 4 ./...
```

`-p 4` limits parallel packages to 4 to avoid spawning too many testcontainers concurrently on a single GitHub runner. SLTs target < 10 min.

## Stage 4: Race Detector

```yaml
race:
  needs: unit
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version: '1.22.x', cache: true }
    - name: race
      run: go test -tags slt -race -count=1 -timeout 20m -p 2 ./...
```

`-race` ~3-5× slowdown; we run a smaller `-p` and accept the cost. Race failures **block the merge**.

## Stage 5: Build

```yaml
build:
  needs: [slt, race]
  runs-on: ubuntu-latest
  outputs:
    image_tag: ${{ steps.image.outputs.tag }}
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version: '1.22.x', cache: true }
    - name: build binaries
      run: |
        CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X main.commit=${{github.sha}}" -o bin/pikshipp ./cmd/server
    - name: image
      id: image
      run: |
        TAG=ghcr.io/pikshipp/pikshipp:${{github.sha}}
        echo "tag=${TAG}" >> $GITHUB_OUTPUT
        docker build -t $TAG -f deploy/Dockerfile .
        docker push $TAG
```

The single binary is the api+worker (selected by `--role=api|worker|all` flag, see HLD §01-architecture/01-monolith-shape).

### Dockerfile

```dockerfile
# deploy/Dockerfile
FROM gcr.io/distroless/static-debian12:nonroot
COPY bin/pikshipp /usr/local/bin/pikshipp
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/pikshipp"]
```

Distroless + nonroot for surface reduction. The migration tool (`migrate`) is a separate image used only by the migration job.

## Stage 6: Migrations Job

Migrations are a **separate** Kubernetes Job that runs on every deploy **before** the new binary rolls out. Failure aborts the deploy.

```yaml
deploy-staging-migrations:
  needs: build
  environment: staging
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - name: run migrations
      run: |
        kubectl apply -f deploy/k8s/migrate-job-staging.yaml
        kubectl wait --for=condition=complete job/migrate-${{github.sha}} --timeout=10m -n pikshipp
        kubectl logs job/migrate-${{github.sha}} -n pikshipp
```

The `migrate-job.yaml` runs `golang-migrate up`. Migrations are forward-only; rollback uses a new "down" migration committed via PR (no live rollbacks).

## Stage 7: Deploy

```yaml
deploy-staging:
  needs: deploy-staging-migrations
  environment: staging
  runs-on: ubuntu-latest
  steps:
    - name: deploy
      run: |
        kubectl set image deployment/pikshipp-api pikshipp=${{needs.build.outputs.image_tag}} -n pikshipp
        kubectl set image deployment/pikshipp-worker pikshipp=${{needs.build.outputs.image_tag}} -n pikshipp
        kubectl rollout status deployment/pikshipp-api --timeout=5m -n pikshipp
        kubectl rollout status deployment/pikshipp-worker --timeout=5m -n pikshipp
```

Rolling deploy with `maxUnavailable=0`, `maxSurge=1` — the new pods come up before old ones drain. Each pod has a `/healthz` (readiness) and `/livez` (liveness) endpoint.

## Stage 8: Smoke Tests

A small set of HTTP checks against the staging environment confirms the binary actually serves:

```bash
curl -fSs https://api.staging.pikshipp.com/healthz
curl -fSs https://api.staging.pikshipp.com/version | jq -e '.commit == "${{github.sha}}"'
go test -tags smoke -timeout 5m ./test/smoke/... -- -url=https://api.staging.pikshipp.com
```

Smoke tests:
- Hit `/healthz` and assert green.
- Login with a known operator account and call `/admin/sellers/health`.
- Submit a tracking webhook for the sandbox carrier and assert the event lands.

If smoke fails, the staging deploy is rolled back automatically (`kubectl rollout undo`).

## Stage 9: Deploy Prod

Production deploy is **manual** — a GitHub environment with required reviewers (engineering lead).

```yaml
deploy-prod:
  needs: smoke
  environment:
    name: production
    url: https://api.pikshipp.com
  runs-on: ubuntu-latest
  steps:
    - name: prod migrations
      run: kubectl apply -f deploy/k8s/migrate-job-prod.yaml ...
    - name: prod deploy
      run: kubectl set image ...
```

The reviewer typically waits for staging to bake for 30 minutes during business hours.

## Branching & Tagging

- **`main`**: always deployable.
- Feature branches off `main`; PR back to `main`.
- We use **trunk-based development** with short-lived branches.
- No `develop` branch.
- Hotfixes go directly off `main` and merge back; same pipeline.
- Tags are `vYYYY.MM.DD-N` (e.g., `v2026.05.14-1`) generated automatically post-prod-deploy.

## Secrets

CI secrets (kubectl config, Docker registry tokens, vendor API keys for SLTs) live in GitHub Actions secrets. They are scoped:

- `lint` / `unit` / `slt` jobs: no secrets needed.
- `build`: docker registry secret only.
- `deploy-staging` / `deploy-prod`: kubectl + cloud creds; gated by `environment:` rules.

The application **never** reads CI secrets directly; runtime secrets come from the cluster's secrets manager (LLD §02-infrastructure/05-secrets).

## Migration Safety Rules

ADR 0009 requires:

1. Migrations are **forward-only** in CI.
2. Every migration must be backwards-compatible with the previous binary version (because new pods boot **after** migrations run; old pods are still serving when the new schema lands).
3. Adding a column? Default to NULL or a value the old binary can ignore.
4. Renaming or dropping? Two-phase: (a) add new, dual-write, (b) drop old in a later migration after binary stops using it.
5. Long-running data migrations (backfills) go in their own river job, not a migration step. The migration only touches schema.

## Failure Modes

| Failure | Detection | Response |
|---|---|---|
| Lint failure | gh action fails | block merge |
| Unit test failure | gh action fails | block merge |
| SLT timeout | gh action timeout | block; investigate (likely flake or container starvation) |
| Race detector report | gh action fails | block; fix race |
| Build fails | gh action fails | block |
| Migration fails on staging | kubectl wait fails | manual investigation; deploy aborted |
| Smoke fails on staging | post-deploy script | auto-rollback; alert on-call |
| Prod migration fails | manual deploy step fails | manual rollback; incident response |

## Local Dev Mirror

Engineers run `make ci-local` which mirrors CI:

```makefile
ci-local: lint unit slt race
lint:
	golangci-lint run
unit:
	go test -count=1 ./...
slt:
	go test -tags slt -count=1 -timeout 15m -p 4 ./...
race:
	go test -tags slt -race -count=1 -timeout 20m -p 2 ./...
```

There is **no other "blessed" local test command**. CI and local use the same Make targets.

## Observability of CI

- Test runs emit a JSON test event log captured to S3 (`reports/ci/<sha>/junit.xml`).
- Flaky tests are surfaced in a weekly digest (Slack channel `#ci-flakes`).
- Slow tests (> 10s) are flagged automatically.

## References

- ADR 0009 (HLD §05-decisions): migrations run as CI step.
- ADR 0008: multi-AZ from day 0.
- LLD §05-cross-cutting/01-testing-patterns: what runs in unit vs SLT.
- LLD §05-cross-cutting/03-deployment: the artifacts CI produces.
- LLD §02-infrastructure/05-secrets: runtime secrets vs CI secrets.
