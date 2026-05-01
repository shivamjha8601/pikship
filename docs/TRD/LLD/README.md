# LLD — Low-Level Design

> Per-component implementation specifications. **A junior engineer should be able to pick up any section and write the code without further architectural input.**

**Status: complete (v0).** Awaits final review pass by Sandeep (Go performance) and Maya (maintainability).

## Reading order for someone implementing

1. **`00-conventions/`** — every Go convention you must follow. Read in full before writing any code.
2. **`01-core/`** — fundamental types every module imports. Implement first.
3. **`02-infrastructure/`** — database setup, HTTP server, configuration, logging. Implement before any domain module.
4. **`03-services/`** — per-domain LLDs. Implement in dependency order (dependencies listed at the top of each file).
5. **`04-adapters/`** — per-vendor adapter implementations (Delhivery, Shopify, etc.).
6. **`05-cross-cutting/`** — testing patterns, CI configuration, deployment scripts, runbook template.

## Index

### 00 Conventions
- [01 Go conventions](00-conventions/01-go-conventions.md)
- [02 Project setup](00-conventions/02-project-setup.md)

### 01 Core
- [01 Money (paise)](01-core/01-money.md)
- [02 IDs](01-core/02-ids.md)
- [03 Clock](01-core/03-clock.md)
- [04 Errors](01-core/04-errors.md)
- [05 Types](01-core/05-types.md)

### 02 Infrastructure
- [01 Database access](02-infrastructure/01-database-access.md)
- [02 Configuration](02-infrastructure/02-configuration.md)
- [03 Observability](02-infrastructure/03-observability.md)
- [04 HTTP server](02-infrastructure/04-http-server.md)
- [05 Secrets](02-infrastructure/05-secrets.md)
- [06 Auth](02-infrastructure/06-auth.md)

### 03 Services
- [01 Policy engine](03-services/01-policy-engine.md)
- [02 Audit](03-services/02-audit.md)
- [03 Outbox](03-services/03-outbox.md)
- [04 Idempotency](03-services/04-idempotency.md)
- [05 Wallet](03-services/05-wallet.md)
- [06 Pricing](03-services/06-pricing.md)
- [07 Allocation](03-services/07-allocation.md)
- [08 Identity](03-services/08-identity.md)
- [09 Seller](03-services/09-seller.md)
- [10 Orders](03-services/10-orders.md)
- [11 Catalog](03-services/11-catalog.md)
- [12 Carriers framework](03-services/12-carriers-framework.md)
- [13 Shipments](03-services/13-shipments.md)
- [14 Tracking](03-services/14-tracking.md)
- [15 NDR](03-services/15-ndr.md)
- [16 COD](03-services/16-cod.md)
- [17 RTO & Returns](03-services/17-rto-returns.md)
- [18 Recon (weight)](03-services/18-recon.md)
- [19 Reports](03-services/19-reports.md)
- [20 Notifications](03-services/20-notifications.md)
- [21 Buyer experience](03-services/21-buyer-experience.md)
- [22 Support](03-services/22-support.md)
- [23 Admin / Ops](03-services/23-admin-ops.md)
- [24 Risk](03-services/24-risk.md)
- [25 Contracts](03-services/25-contracts.md)

### 04 Adapters
- [01 Delhivery (carrier reference)](04-adapters/01-delhivery.md)
- [02 Sandbox carrier (test)](04-adapters/02-sandbox-carrier.md)
- [03 Shopify (channel)](04-adapters/03-shopify-channel.md)
- [04 CSV / Manual (channel)](04-adapters/04-csv-channel.md)
- [05 MSG91 (SMS)](04-adapters/05-msg91-sms.md)
- [06 Google OAuth](04-adapters/06-google-oauth.md)
- [07 AWS SES (email)](04-adapters/07-aws-ses.md)
- [08 S3 object store](04-adapters/08-s3-objstore.md)

### 05 Cross-cutting
- [01 Testing patterns](05-cross-cutting/01-testing-patterns.md)
- [02 CI / CD](05-cross-cutting/02-ci-cd.md)
- [03 Deployment](05-cross-cutting/03-deployment.md)
- [04 Runbook template](05-cross-cutting/04-runbook-template.md)

## Conventions for every LLD

Each LLD section follows the same template:

1. **Purpose** — one paragraph, what this module does and why.
2. **Dependencies** — what other modules / packages this depends on.
3. **Package layout** — the file structure: what goes where.
4. **Public API** — exact Go interface(s) with godoc comments.
5. **Internal types** — structs, enums, constants.
6. **Database schema** — DDL (CREATE TABLE, indexes, RLS policies).
7. **SQL queries** — sqlc-style; the actual SQL we'll execute.
8. **Implementation notes** — algorithms, locking, concurrency, edge cases.
9. **Error handling** — sentinel errors, when to wrap vs. return.
10. **Configuration** — env vars, defaults, runtime tunables.
11. **Testing** — SLT skeleton, unit test examples, benchmarks.
12. **Observability** — log lines, metrics, audit emission.
13. **Performance budget** — target latencies; what we measure.
14. **Open questions** — unknowns surfaced for review.

## Status

| Section | Status |
|---|---|
| 00 Conventions | Complete |
| 01 Core | Complete |
| 02 Infrastructure | Complete |
| 03 Services | Complete (25 services) |
| 04 Adapters | Complete (8 adapters) |
| 05 Cross-cutting | Complete |
| Final review (Sandeep / Maya) | In progress |

## Authorship discipline

- Every LLD section is **commitable** as a standalone PR.
- New LLDs are reviewed by ≥ 1 lead engineer before any code is written against them.
- LLDs evolve with the code: when implementation reveals a gap or wrong assumption, the LLD is updated **first**, then the code.
