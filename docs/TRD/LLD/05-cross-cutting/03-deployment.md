# Deployment

## Purpose

This document describes how Pikshipp runs in staging and prod: the topology, the pods, the configuration surface, the boot sequence, the health checks, and the rollback procedure.

The architectural decisions live in the HLD (multi-AZ from day 0, Postgres-only data plane, single binary with `--role` flag, migrations in CI). This LLD is the **operator's manual** for actually running the thing.

## Topology

```
                     ┌──────────────────────┐
                     │   AWS ALB / Cloudflare │
                     └──────────┬─────────────┘
                                │
                     ┌──────────┴─────────────┐
                     │                        │
              ┌──────▼────────┐       ┌──────▼────────┐
              │ pikshipp-api  │       │ pikshipp-api  │   (N≥2; multi-AZ)
              │   pod (AZ-a)  │       │   pod (AZ-b)  │
              └──────┬────────┘       └──────┬────────┘
                     │                        │
                     │  pgxpool                │
              ┌──────▼────────────────────────▼───────┐
              │             RDS Postgres 16            │
              │             primary + standby          │
              │             multi-AZ                   │
              └────────────────────┬───────────────────┘
                                   │
                     ┌─────────────┴───────────────┐
                     │                              │
              ┌──────▼─────────┐            ┌──────▼─────────┐
              │ pikshipp-worker│            │ pikshipp-worker│   (N≥2; multi-AZ)
              │   pod (AZ-a)   │            │   pod (AZ-b)   │
              └────────────────┘            └────────────────┘
                     ▲                                  ▲
                     │                                  │
                     └──────── river queue (Postgres) ──┘
```

- **`pikshipp-api`**: serves HTTP. Stateless. N=2 minimum (multi-AZ).
- **`pikshipp-worker`**: runs river jobs. Stateless. N=2 minimum.
- **RDS Postgres**: multi-AZ from day 0 (ADR 0008).
- **S3 bucket**: cross-region object storage (LLD §04-adapters/08).
- **Vector → CloudWatch**: log shipping from day 0 (ADR 0011).

## Single Binary, Two Roles

```bash
# api role: HTTP server, no river workers
pikshipp --role=api --config=/etc/pikshipp/config.yaml

# worker role: river workers + scheduled jobs, no HTTP listener
pikshipp --role=worker --config=/etc/pikshipp/config.yaml

# all role: both. Used in dev / single-instance deployments.
pikshipp --role=all --config=/etc/pikshipp/config.yaml
```

The role flag turns features on/off in `cmd/server/main.go`. There's no separate worker binary — same image, same code, different startup.

## Boot Sequence

```
1. parse flags + load config
2. set up logger (slog → JSON → stdout)
3. set up metrics (otel + Prometheus exporter on :9100)
4. open Postgres pool
5. health-check Postgres (SELECT 1)
6. construct services (wire.go) — pure function composition, no I/O
7. health-check vendor adapters (S3, SES, MSG91 if reachable)
8. if role in {api, all}: start HTTP server on :8080
9. if role in {worker, all}: start river client, register workers, start sweepers
10. start LISTEN/NOTIFY consumers (carrier_health, contract_active_changed, etc.)
11. signal readiness (`/healthz` returns 200)
```

A failure at any step **aborts boot** with a non-zero exit code. Kubernetes restarts the pod with backoff.

## Configuration

Config is loaded from a YAML file referenced by `--config`. Secrets are interpolated from environment variables (LLD §02-infrastructure/05-secrets) — never written to disk.

```yaml
# /etc/pikshipp/config.yaml (k8s ConfigMap)
env: prod
region: ap-south-1
http:
  addr: ":8080"
  timeout: 30s
postgres:
  url: ${PIKSHIPP_POSTGRES_URL}
  max_conns: 50
  min_conns: 5
  stmt_cache_capacity: 200
secrets:
  carrier_delhivery: ${PIKSHIPP_DELHIVERY_API_KEY}
  carrier_delhivery_webhook: ${PIKSHIPP_DELHIVERY_WEBHOOK_SECRET}
  msg91_auth: ${PIKSHIPP_MSG91_AUTH_KEY}
  ses_access_key: ${PIKSHIPP_SES_ACCESS_KEY}
  ses_secret_key: ${PIKSHIPP_SES_SECRET_KEY}
  google_oauth_client_secret: ${PIKSHIPP_GOOGLE_CLIENT_SECRET}
  s3_access_key: ${PIKSHIPP_S3_ACCESS_KEY}
  s3_secret_key: ${PIKSHIPP_S3_SECRET_KEY}
carriers:
  delhivery:
    base_url: https://track.delhivery.com
sandbox_carrier_enabled: false
s3:
  bucket: pikshipp-prod-blobs
  region: ap-south-1
ses:
  region: ap-south-1
  default_from: noreply@pikshipp.com
  config_set: pikshipp-prod
google_oauth:
  client_id: ${PIKSHIPP_GOOGLE_CLIENT_ID}
  redirect_uri: https://api.pikshipp.com/auth/google/callback
tracking:
  public_base_url: https://track.pikshipp.com
session:
  cookie_domain: .pikshipp.com
  ttl: 720h
log:
  level: info
  format: json
```

Configuration changes require a deploy. There is no live reload.

## Kubernetes Manifests (excerpts)

```yaml
# deploy/k8s/api.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pikshipp-api
  namespace: pikshipp
spec:
  replicas: 2
  strategy:
    type: RollingUpdate
    rollingUpdate: { maxUnavailable: 0, maxSurge: 1 }
  selector:
    matchLabels: { app: pikshipp, role: api }
  template:
    metadata:
      labels: { app: pikshipp, role: api }
    spec:
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: topology.kubernetes.io/zone
          whenUnsatisfiable: DoNotSchedule
          labelSelector: { matchLabels: { app: pikshipp, role: api } }
      containers:
        - name: pikshipp
          image: ghcr.io/pikshipp/pikshipp:GIT_SHA
          args: ["--role=api", "--config=/etc/pikshipp/config.yaml"]
          ports:
            - { name: http, containerPort: 8080 }
            - { name: metrics, containerPort: 9100 }
          envFrom:
            - secretRef: { name: pikshipp-secrets }
          volumeMounts:
            - { name: config, mountPath: /etc/pikshipp, readOnly: true }
          readinessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 5
            periodSeconds: 5
            timeoutSeconds: 2
            failureThreshold: 3
          livenessProbe:
            httpGet: { path: /livez, port: 8080 }
            initialDelaySeconds: 30
            periodSeconds: 30
            timeoutSeconds: 5
            failureThreshold: 6
          resources:
            requests: { cpu: 250m, memory: 256Mi }
            limits:   { cpu: 1500m, memory: 1Gi }
      volumes:
        - name: config
          configMap: { name: pikshipp-config }
```

`pikshipp-worker` is similar with `--role=worker` and slightly higher CPU requests (background work).

## Health Endpoints

| Endpoint | Purpose |
|---|---|
| `/healthz` | Readiness — checks Postgres reachable + vendor health-check passing. Returns 503 during graceful drain. |
| `/livez` | Liveness — minimal "Go runtime healthy" check. Doesn't touch DB. |
| `/version` | Returns `{"commit": "<sha>", "built_at": "..."}`. Used by smoke tests. |
| `/metrics` (port 9100) | Prometheus format. Not exposed externally. |

## Graceful Shutdown

On SIGTERM:

```go
1. /healthz starts returning 503 (5s drain delay so LB notices)
2. http.Server.Shutdown(ctx) with 25s deadline
3. river.Client.Stop() — finishes in-flight jobs
4. close pgxpool
5. exit 0
```

Kubernetes `terminationGracePeriodSeconds: 60` gives us margin. The 5s drain delay before shutdown lets the load balancer remove the pod before connections drop.

## Migrations

Per ADR 0009, migrations run as a Kubernetes Job **before** the rollout starts:

```yaml
# deploy/k8s/migrate-job.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-GIT_SHA
  namespace: pikshipp
spec:
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: ghcr.io/pikshipp/migrate:GIT_SHA
          command: ["migrate", "-source", "file:///migrations", "-database", "$(PIKSHIPP_POSTGRES_URL)", "up"]
          envFrom:
            - secretRef: { name: pikshipp-secrets }
```

Job failure aborts the deploy. The job is idempotent (golang-migrate tracks state in `schema_migrations`).

## Rollback Procedure

### Code-only rollback

If the new binary misbehaves and the schema is **backwards compatible** with the prior version:

```bash
kubectl rollout undo deployment/pikshipp-api -n pikshipp
kubectl rollout undo deployment/pikshipp-worker -n pikshipp
```

Time to recovery: ~2 minutes.

### Migration-included rollback

If the new release shipped a migration that the old binary cannot tolerate:

1. **Don't** run `migrate down`. Migrations are forward-only.
2. Open a hotfix PR that adds a new migration to **revert** the schema change OR adapts the new binary to be compatible.
3. Ship through normal pipeline.

This is why migrations must be **backwards-compatible by construction** (LLD §05-cross-cutting/02-ci-cd, §Migration Safety Rules).

### Database disaster

Worst case: RDS multi-AZ failover. Automatic; ~60-120s downtime. The application's pgxpool reconnects automatically. We **don't** point-in-time restore as a deploy rollback — only for true data corruption events, manually.

## Capacity Planning Snapshot

Initial sizing (one prod cluster):

| Component | v0 size | Headroom for | Scaling trigger |
|---|---|---|---|
| `pikshipp-api` | 2 × (1 vCPU, 1 GiB) | ~50 RPS | CPU > 60%, scale to 4 |
| `pikshipp-worker` | 2 × (1.5 vCPU, 1 GiB) | ~5k jobs/min | river queue depth > 1000 |
| RDS | db.r6g.large multi-AZ (2 vCPU, 16 GiB) | ~500 QPS | replication lag, IOPS, CPU |
| S3 | (no sizing) | unlimited | n/a |

Reference HLD §04-cross-cutting/04-capacity-planning for the full forecast.

## Environments

| Env | URL | Postgres | Replicas | Sandbox carrier | Risk |
|---|---|---|---|---|---|
| `dev` | `localhost` | docker-compose | 1 (--role=all) | enabled | none |
| `staging` | `api.staging.pikshipp.com` | RDS small, multi-AZ | 2+2 | enabled | low; CI auto-deploys |
| `prod` | `api.pikshipp.com` | RDS large, multi-AZ | 2+2 (auto-scale) | **disabled** | manual approval gate |

Promoting between environments is a deploy of the **same image** with different config.

## Observability

- Logs: stdout JSON → Vector sidecar → CloudWatch Logs (per-pod stream).
- Metrics: Prometheus scrape `:9100/metrics`; Grafana dashboards live in IaC.
- Traces: OpenTelemetry → Honeycomb (or AWS X-Ray); 10% sampling.
- Alerts: Grafana Alertmanager → PagerDuty for critical; Slack for warnings.

The dashboards we run from day 0:
- API request rate, p50/p99 latency, error rate.
- River job throughput + queue depth.
- Postgres replication lag, deadlock rate, connection count.
- Carrier health (per-carrier breaker state).
- Wallet invariant check failures.
- COD remittance lag.
- NDR open-case count.

## Disaster Recovery Pointers

- **Backups**: RDS automated snapshots, 14-day retention. Manual snapshot before any large schema change.
- **Restore drill**: quarterly; restore latest snapshot to a sandbox env and verify booking flow.
- **Cross-region**: not v0. v1+ adds CRR for S3 KYC paths and RDS read-replica in a second region.
- **Runbook**: LLD §05-cross-cutting/04-runbook-template.

## References

- HLD §01-architecture/01-monolith-shape: single-binary, role-flag.
- HLD §04-cross-cutting/04-capacity-planning: sizing.
- HLD §05-decisions: ADRs 0008 (multi-AZ), 0009 (migrations as CI step), 0010 (env-var secrets), 0011 (Vector + CloudWatch).
- LLD §02-infrastructure/02-configuration: config struct.
- LLD §02-infrastructure/05-secrets: secret loading.
- LLD §05-cross-cutting/02-ci-cd: pipeline that produces these artifacts.
