# ADR 0001 — Modular monolith, not microservices

Date: 2026-04-30
Status: Accepted
Owner: Architect A

## Context

Pikshipp at v0 is one developer + 5–10 friendly sellers. By v1 it's small team + a few hundred sellers. By v2 it's mid-team + thousands of sellers. The architectural shape we choose affects how fast we ship, how well we scale, and how easy it is to onboard engineers.

Two extremes:
- Microservices from day 1. N services, each with its own DB, deployed independently.
- Single monolith. One binary. One DB.

Most common middle ground: modular monolith. One binary, but internally factored into modules with strict package boundaries that map to bounded contexts.

## Decision

**Modular monolith.** One Go binary. One database. Internal packages map to PRD bounded contexts. Cross-package dependencies only through published interfaces. No service-mesh, no event-bus, no inter-service RPC.

Per-feature shape inside the monolith:
```
internal/<module>/
├── service.go        — public interface (THE module API)
├── service_impl.go   — implementation
├── repo.go           — DB access (private)
├── jobs.go           — river job handlers (private)
└── service_slt_test.go
```

## Alternatives considered

### Microservices from day 1
- Rejected: at our scale, the cost of N services (deploys, networking, dist tracing, schema migrations across services, eventual consistency everywhere) far exceeds the benefit.
- One developer cannot operate 12 services. Even three is heavy.
- We don't have a scaling problem at v0/v1. Microservices solve a problem we don't have.

### Modular monolith with future split (our choice)
- Strict package boundaries make a future service split mechanical, not a rewrite.
- All the benefits of "one repo, one binary, one deploy" today.
- When (if) we hit a real reason to split (one module needs different scaling, different language, different team ownership), we lift it out cleanly.

### Pure monolith (no internal modularity)
- Rejected: in 12 months it becomes a tar pit. Every new feature touches every file. Test isolation breaks. Deploy fear sets in.

## Consequences

### What this commits us to
- Strict package boundaries. Enforced via lint (`depguard`) and code review.
- One DB shared across modules. Cross-module joins are allowed (and useful) but not abused.
- One deploy artifact. One systemd service. One CI pipeline.
- Modules can be added without infra changes.

### What it costs
- Discipline to maintain boundaries; cheap if we lint, expensive if we don't.
- One module's bug can affect another (compared to true microservices). Mitigated by tests and audit.
- No language polyglot — Go for everything (acceptable for our team size).

### What it enables
- Single-dev productivity at v0 (no service ops overhead).
- Easy refactoring across module boundaries (vs. cross-service contract negotiation).
- A clear future migration path to services if/when justified.

## Open questions

- When (if ever) should we split? Triggers: a module needs >2x the capacity of others; a module has different on-call rotation; a module is rewritten in a different language; we hit deploy-frequency conflict where one module needs hourly deploys and another needs careful daily deploys.
- These are all v3+ concerns. We're not splitting at v1 or v2.
