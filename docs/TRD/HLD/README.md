# HLD — High-Level Design

> Service decomposition, infra choices, physical data model, cross-cutting concerns. The bridge from PRD to code.

**Status: ⏳ not yet authored.**

Derives from: [`../../PRD/`](../../PRD/) — every HLD doc must trace back to one or more PRD docs.

## Proposed structure (to be filled in)

```
HLD/
├── README.md                          ← this file
├── 00-overview.md                     ← cross-references to PRD; map of HLD docs
│
├── 01-system-architecture/            ← service decomposition, infra choices
│   ├── 01-services.md                 ← list of services + their bounded contexts
│   ├── 02-async-boundaries.md         ← queues, event bus, async patterns
│   ├── 03-storage.md                  ← database choices, sharding, caching
│   ├── 04-deployment.md               ← regions, AZs, k8s/ECS, CI/CD
│   └── 05-network-and-edge.md         ← LB, CDN, WAF, DNS
│
├── 02-data-model/                     ← physical data model
│   ├── 01-schema-conventions.md       ← naming, IDs, tenancy enforcement
│   ├── 02-tables.md                   ← per-table schemas
│   ├── 03-indexes-and-partitioning.md
│   └── 04-migrations.md               ← migration strategy
│
├── 03-services/                       ← per-service HLD (one .md per service)
│   ├── 01-identity.md
│   ├── 02-orders.md
│   ├── 03-allocation.md
│   ├── 04-pricing.md
│   ├── 05-shipments.md
│   ├── 06-tracking.md
│   ├── 07-ndr.md
│   ├── 08-rto-returns.md
│   ├── 09-cod.md
│   ├── 10-wallet.md
│   ├── 11-weight-recon.md
│   ├── 12-notifications.md
│   ├── 13-buyer-experience.md
│   ├── 14-support.md
│   ├── 15-admin.md
│   ├── 16-risk.md
│   ├── 17-contracts.md
│   ├── 18-audit.md
│   ├── 19-policy-engine.md
│   ├── 20-channel-adapters.md
│   ├── 21-carrier-adapters.md
│   └── 22-reports.md
│
├── 04-cross-cutting/                  ← spans every service
│   ├── 01-auth-and-rbac.md
│   ├── 02-observability.md
│   ├── 03-security.md
│   ├── 04-secrets-and-kms.md
│   ├── 05-resilience.md               ← circuit breakers, retries, idempotency
│   ├── 06-disaster-recovery.md
│   └── 07-rate-limiting.md
│
├── 05-integrations/                   ← realization of adapter frameworks
│   ├── 01-channel-adapter.md
│   ├── 02-carrier-adapter.md
│   ├── 03-payment-gateway.md
│   ├── 04-comms-providers.md
│   └── 05-kyc-vendors.md
│
├── 06-decisions/                      ← ADRs (Architecture Decision Records)
│   ├── README.md                      ← ADR template + index
│   └── 0001-*.md                      ← one file per decision
│
└── diagrams/                          ← .mmd + .png
```

## When this gets authored

After the PRD is approved as v1.0 (which it now is) and engineering leadership is ready to commit to the technical approach. Expected:
- `00-overview.md` and `01-system-architecture/` first.
- `02-data-model/` next.
- `03-services/` per-service as eng owners are assigned.
- `04-cross-cutting/` and `05-integrations/` in parallel.
- `06-decisions/` accumulates throughout.

## Conventions (will apply to every HLD doc)

- Cross-references to PRD via relative path: `[Feature 13](../../PRD/04-features/13-wallet-and-billing.md)`.
- Each service doc must contain: responsibility, dependencies, public API surface, internal data model summary, scaling considerations, failure modes, observability hooks.
- ADR format: short, dated, single decision per file.
- Open questions tagged `Q-HLD-<area>-<n>`.

## Open questions (to be raised once authoring begins)

- (none yet)
