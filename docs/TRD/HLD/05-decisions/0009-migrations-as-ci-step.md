# ADR 0009 — Migrations as a CI step, not on startup

Date: 2026-04-30
Status: Accepted
Owner: Architect A (after Architect C push-back)
Supersedes: prior conversational decision "migrations on startup"

## Context

Original idea: binary on startup runs `migrate up` (gated by Postgres advisory lock). Simple; one-step deploy.

Architect C objected:
1. Bad migrations take the service down for the duration of the rollback (which involves SSH + manual `migrate down`).
2. Long migrations block the binary from serving while running.
3. Auto-running migrations on startup couples deploy success to migration success.

Better: run migrations as a separate CI step before deploying the new binary. Binary on startup verifies schema version is high enough and refuses to start if not.

## Decision

**Migrations are run as a separate CI step before binary deploy.** Binary on startup checks `schema_migrations.version >= compiled_target` and exits with error if not.

CI pipeline:
1. PR has migration → CI runs `migrate up` against ephemeral PG.
2. CI runs `migrate down 1; migrate up 1` (reversibility check).
3. Lint check: every `up.sql` has a corresponding `down.sql`.
4. On merge to main: deploy pipeline runs `migrate up` against staging, then prod, then deploys new binary.
5. Binary on boot: `SELECT version FROM schema_migrations LIMIT 1;` — if `version < target`, log error and exit.

## Alternatives considered

### Migrations on startup (original)
- Rejected per Architect C's reasoning: bad migration takes the service down; long migrations block boot.

### Migrations as a separate command, run by ops manually
- Rejected: too easy to forget; deploys fail surprisingly.

### CI-step migrations + binary check (chosen)
- Reversibility: rollback the binary without rolling back schema (assuming forward-compatible migrations).
- Observability: migrations run as a CI step; output visible.
- Safety: long migrations don't block the binary.

## Consequences

### What this commits us to
- Migrations must be **forward-compatible**: the previous binary version must still work on the new schema (during the brief window between migration and binary deploy).
- All schema changes follow expand/contract: add column → backfill → swap reads → drop old column over multiple releases.
- CI pipeline complexity: migrations as a deploy step.

### What it costs
- Discipline around expand/contract for schema changes.
- One more step in deploy pipeline.

### What it enables
- Reversibility: binary can be rolled back without affecting schema.
- Observability: migration output is in CI logs.
- Safety: bad migration is detected before binary is deployed; service is unaffected.
- Matches industry practice for any platform with serious deploy hygiene.

## Operational notes

- `pikshipp_migration` Postgres role used for migrations; has DDL permissions.
- Migration files in `migrations/`, `golang-migrate` format.
- CI deploys migration *atomically* — either all pending migrations apply or none.
- Long migrations (>30s): warned in PR review; consider if expand/contract is needed.
- Production migration application: after staging validation, manual approval required.

## Lint rules

- Every `up.sql` has a corresponding `down.sql`. CI fails otherwise.
- No `DROP TABLE`, `DROP COLUMN`, or `ALTER COLUMN` (data-changing) without an ADR justification in the PR description.

## Open questions

- For very long migrations (e.g., backfilling a large column), do we have a "low-priority migration" track that runs without blocking? Possibly; not v0.
- If a production migration fails partway: explicit rollback plan + ops paged. Document procedure in a runbook.
