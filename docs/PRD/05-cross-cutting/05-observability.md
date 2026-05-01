# Observability

## Scope

If we can't observe it, we can't operate it. The platform's complexity (24 features × multi-tenant × N carriers × N channels) demands first-class observability across logs, metrics, traces, and business KPIs.

## Three layers

### 1. Infra observability
- Host / container metrics (CPU, mem, disk, network).
- Cloud provider dashboards.
- Alerting on infra-level failures.

### 2. Application observability
- Structured logs with tenant + request id correlation.
- Distributed tracing across services.
- Application metrics (RED — rate, errors, duration; USE — utilization, saturation, errors).
- Per-tenant metrics where meaningful.

### 3. Business observability
- North-star metric and tier 1/2/3 KPIs (per `01-vision-and-strategy/04-success-metrics.md`).
- Operational signals (carrier health, KYC backlog, dispute volume, etc.).
- Real-time dashboards in admin console.

## Hard rules

- Every log line carries: `seller_id` (if applicable), `request_id`, `user_id` (if applicable), `actor_kind`, `service`, `version`.
- Every external API call (carrier, PG, channel) is traced with timing and outcome.
- Every webhook (in/out) logs payload reference (not raw payload in logs — separately stashed).
- PII never in logs; redact at logger.
- Every alert has a runbook.
- Every alert has an owner.
- Alert fatigue actively combated; thresholds reviewed monthly.

## Telemetry stack (logical, not vendor-locked)

| Layer | Open-source / vendor options |
|---|---|
| Logs | OpenSearch / ELK / Datadog Logs |
| Metrics | Prometheus + Grafana / Datadog Metrics |
| Traces | OpenTelemetry → Jaeger / Tempo / Datadog APM |
| Errors | Sentry / Rollbar |
| Real-time dashboards | Grafana / Datadog |
| Warehouse | BigQuery / Snowflake / Redshift |
| BI | Metabase / Looker (internal) |

(Vendor choice is TRD scope.)

## Tenant slicing

Every metric must be **sliceable by tenant** (reseller and seller). The data warehouse denormalizes tenancy on every fact table. Examples:

- Booking success rate per (tenant, carrier).
- API latency per (tenant, endpoint).
- Tracking event lag per (tenant, carrier).

## Alert categories

| Category | Examples | Owner | SLA |
|---|---|---|---|
| P0 (page) | Platform-wide outage, ledger inconsistency, tenant data leak signal | On-call eng + ops | 15 min ack |
| P1 | Carrier outage, KYC vendor outage, comms vendor outage | Ops + eng | 1 h ack |
| P2 | Alert thresholds (NDR spike, dispute spike) | Ops or PM | 4 h |
| P3 / informational | Daily digests, capacity warnings | PM / leadership | next business day |

## Dashboards

- **Exec dashboard** — Tier 1 KPIs.
- **Ops dashboard** — Tier 2/3, queue depths, alerts.
- **Eng dashboard** — Infra + application metrics.
- **Tenant-specific dashboards** — for resellers, scoped to their tenant.

## Cost governance

- Log retention tiered (hot 7d, warm 30d, cold 90d, then archive).
- Metric cardinality monitored; high-cardinality labels reviewed.
- Trace sampling intelligent (errors always sampled; baseline 1%).

## Open questions

- **Q-OB1** — Self-hosted vs vendor (Datadog/Sentry)? Default: vendor v1 for speed; revisit.
- **Q-OB2** — Tenant-scoped Grafana exposure to resellers? Default: limited curated dashboards; not raw access.
- **Q-OB3** — PII in traces — how strict? Default: redacted by default; opt-in field allow-list.
