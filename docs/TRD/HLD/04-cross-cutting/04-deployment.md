# Cross-cutting: Deployment

> v0: single EC2 + RDS multi-AZ. Migrations as CI step. Vector → CloudWatch. Deploy outage acceptable. v1: multi-instance + ALB + zero-downtime.

## v0 topology

```
                     ┌──────────────┐
                     │  Route 53    │
                     └──────┬───────┘
                            │
                     ┌──────▼───────┐
                     │  ACM cert    │
                     │  + ALB (TLS) │
                     └──────┬───────┘
                            │
                     ┌──────▼─────────────────────────┐
                     │  EC2 (single instance)         │
                     │  ─ pikshipp.service (systemd)  │
                     │    --role=all                  │
                     │  ─ Vector agent                │
                     │  ─ TLS-terminating proxy?      │
                     │    (No — ALB does it)          │
                     └──────┬─────────────────────────┘
                            │
              ┌─────────────┼──────────────┐
              │             │              │
       ┌──────▼─────┐ ┌─────▼─────┐  ┌─────▼─────┐
       │  RDS PG    │ │  S3       │  │ CloudWatch │
       │  multi-AZ  │ │  bucket   │  │  Logs      │
       └────────────┘ └───────────┘  └────────────┘
                            ▲
                            │
                     Vector ships logs
```

## Components

### EC2 instance
- **Type**: `t4g.medium` at v0 (4GB RAM, 2 vCPU). Upgrade based on load.
- **OS**: Amazon Linux 2023 (or Ubuntu 22.04 LTS — pick one in the OPS doc).
- **Storage**: 20GB gp3 EBS, encrypted (only host-level; not application).
- **AZ**: `ap-south-1a`.
- **IAM role**: scoped to RDS connect, S3 read/write on our bucket, CloudWatch logs put.

### RDS Postgres
- **Engine**: PG 16.x.
- **Size**: `db.t4g.medium` (4GB, 2 vCPU). Upgrade as needed.
- **Multi-AZ**: yes from day 0.
- **Storage**: 100GB gp3, auto-scaling enabled.
- **Backups**: automated, 7-day retention.
- **Maintenance window**: Sundays 02:00–04:00 IST.
- **Parameter group**: PG defaults; only override `shared_preload_libraries` if needed for extensions.

### S3
- **Bucket**: `pikshipp-<env>` (one per env: dev/staging/prod).
- **Region**: `ap-south-1`.
- **Versioning**: on (so we can recover from accidental deletes).
- **Lifecycle**:
  - `tracking-raw/` → expire 90d.
  - `kyc-docs/` → IA after 90d, retain per legal.
  - `labels/` → expire 90d post-shipment-terminal.

### ALB
- TLS termination via ACM cert.
- Routes:
  - `/healthz`, `/readyz` → EC2 instance.
  - Everything else → EC2 instance.
- Health check: `/readyz` every 30s.

## Build & deploy pipeline

### CI (GitHub Actions)

On PR push:
1. `go vet ./...`
2. `golangci-lint run`
3. `go test ./...` (unit tests)
4. `make slt` (SLT against testcontainers)
5. `make bench` (regression check)
6. Build binary; upload as PR artifact.

On merge to `main`:
1. All of the above.
2. Build production binary (static, with `CGO_ENABLED=0`).
3. Tag with git SHA; upload to S3 (build artifacts).
4. Manual approval to deploy.
5. **Run `migrate up` against staging.**
6. Deploy binary to staging EC2.
7. Smoke test: `curl /readyz` returns 200.
8. **Run `migrate up` against prod.** (Pause for review.)
9. Deploy binary to prod.
10. Tag the release in Git.

### Migration is a separate CI step

```
PR has migration?
  → CI runs migrate up against ephemeral PG.
  → CI runs migrate down 1 + migrate up 1 (verifies reversibility).
  → CI lint-checks: every up.sql has a corresponding down.sql.
```

On deploy:
- `migrate up` runs as a CI step before binary deploy.
- New binary expects schema version >= compiled-in target.
- Binary refuses to start if schema is older.

This means:
- Bad migration is detected before binary is rolled.
- Binary rollback is possible (schema is forward-compatible by design).
- No "migration runs at startup" race conditions.

## v0 deploy outage

A v0 deploy looks like:
1. Stop `pikshipp.service` (SIGTERM).
2. Wait for graceful shutdown (drains in-flight requests + jobs; up to 30s).
3. Replace binary on disk.
4. Start `pikshipp.service`.
5. Wait for `/readyz` to return 200 (typically 5–10s).

**Total outage**: ~30–45 seconds.

**Mitigations**:
- Deploy outside business hours.
- Communicate with friendly sellers ahead of major releases.
- v0 traffic is low; the impact is small.

**v1 fix**: blue/green deploy on multi-instance ALB; zero downtime.

## v1 topology (preview)

```
ALB ─┬─► EC2 (--role=api, instance 1)
     ├─► EC2 (--role=api, instance 2)
     │
     └─► (worker is a separate EC2 instance --role=worker; not behind ALB)

RDS ── multi-AZ; same as v0 but maybe larger.
```

Migration to v1 is config + provisioning; no code change.

## Configuration

All via env vars. `pikshipp.service` reads from `/etc/pikshipp.env`:

```
PIKSHIPP_ROLE=all
PIKSHIPP_DB_URL=postgres://...
PIKSHIPP_S3_REGION=ap-south-1
PIKSHIPP_S3_BUCKET=pikshipp-prod
PIKSHIPP_GOOGLE_OAUTH_CLIENT_ID=...
PIKSHIPP_GOOGLE_OAUTH_CLIENT_SECRET=...
PIKSHIPP_SESSION_HMAC_KEY=...
PIKSHIPP_RAZORPAY_KEY=...
PIKSHIPP_RAZORPAY_SECRET=...
PIKSHIPP_DELHIVERY_API_KEY=...
PIKSHIPP_MSG91_AUTH_KEY=...
PIKSHIPP_SES_FROM_EMAIL=notify@pikshipp.com
PIKSHIPP_LOG_LEVEL=info
PIKSHIPP_VERSION=<git-sha>
```

At v0 these are plain text. **Note**: revisit before prod (move to SSM/Secrets Manager).

## Secrets handling at v0

- Stored in `/etc/pikshipp.env` (mode 600, owned by `pikshipp` user).
- Provisioned via Terraform output → user-data script.
- Rotation requires manual update + service restart.

**Acknowledged technical debt**: at v1 / pre-prod, migrate to SSM Parameter Store. The application reads via AWS SDK on startup; rotation is a config-flip + service restart.

## Disaster recovery

### Backups
- RDS automated backups: daily, 7-day retention.
- Manual snapshot before each major release.
- S3 versioning preserves accidental deletes.

### Restore drill
- **Quarterly**: spin up RDS instance from snapshot; verify DB integrity; verify our binary connects.
- **At least once per year**: full recovery test (binary + DB) against snapshot.

### RPO / RTO targets
- **RPO**: 5 minutes (RDS automated backup interval).
- **RTO**: 30 minutes (multi-AZ failover) at v0; 15 minutes at v1.

## Observability hooks at deploy time

- Vector running on the EC2 host; ships logs to CloudWatch from boot.
- CloudWatch Alarms configured via Terraform: error rate, queue depth, RDS connection count, RDS CPU, EC2 CPU.

## Local dev

```
docker compose up postgres localstack
make migrate-up
make run
```

Local dev runs `--role=all` against local PG and LocalStack S3. Same code paths as prod.

## Staging environment

- Smaller EC2 + RDS.
- Same deployment pipeline; staging deploy precedes prod.
- Used for: sandbox carrier testing, friendly seller pre-prod, manual smoke testing.

## Production access

- SSH via bastion host (IP-allowlisted).
- Direct SSH to EC2 disabled.
- DB access via SSM Session Manager (no DB credentials shared).
- All access audit-logged via CloudTrail.

## What we DON'T do at v0

- Auto-scaling (single instance).
- Multi-region (Mumbai only).
- Blue-green (accept the outage).
- IaC for everything (some manual setup; Terraform for the load-bearing pieces).

These add complexity that isn't justified at v0. v1 / v2 add them as needed.
