# IMPL — Implementation plan & tracking

> Living documents that capture what's actively being built, in what order, and how it's running in production once shipped.

**Status: ⏳ not yet authored.**

Derives from: [`../PRD/08-roadmap/`](../PRD/08-roadmap/) (the PRD-side phasing) and from HLD/LLD as those land.

## What this folder is for

This is **high-churn**. Unlike PRD/HLD/LLD which represent stable design intent, IMPL is the operational reality — sprint plans, build progress, decisions made under time pressure, runbooks for ops.

```
IMPL/
├── README.md                  ← this file
├── 01-v0-plan.md              ← v0 internal alpha: what gets built, in what order
├── 02-v1-plan.md              ← v1 public launch: scope per sprint
├── 03-v2-plan.md              ← later
├── 04-v3-plan.md              ← later
│
├── milestones/                ← release notes per milestone
│   └── ...
│
├── runbooks/                  ← operational runbooks
│   ├── README.md
│   ├── stuck-shipment.md
│   ├── carrier-outage.md
│   ├── kyc-escalation.md
│   ├── wallet-incident.md
│   └── ...
│
├── retros/                    ← post-release retrospectives
│   └── ...
│
└── deferred-decisions.md      ← LLD/HLD questions deferred during implementation
```

## What it is NOT

- **Not the source of truth for product scope** — that's PRD.
- **Not the source of truth for technical design** — that's HLD/LLD.
- **Not a project management tool replacement** — Linear / Jira / GitHub Projects are for tickets. This folder captures the *narrative*: "what we did, what we decided, why".

## What it IS

- The phased roadmap rendered as actionable plan: what eng work happens in what order.
- Runbooks for operational scenarios (mostly authored *after* features ship and ops encounters real-world cases).
- Post-release retrospectives capturing what we learned.
- "Deferred decisions" — design questions raised during implementation that we punted on, with target resolution date.

## When this gets started

- **`01-v0-plan.md`** when v0 build kicks off (immediately after HLD overview is in place).
- **Runbooks** as operational scenarios occur in v1+ (ops authors them).
- **Retros** after each release.

## Conventions

- Plans reference PRD features by number (`PRD/04-features/13-wallet-and-billing.md`).
- Runbooks reference admin console actions (`PRD/04-features/19-admin-and-ops.md`) and audit events (`PRD/05-cross-cutting/06-audit-and-change-log.md`).
- Plans should explicitly cite the v0/v1/v2/v3 cut from `PRD/08-roadmap/`.
