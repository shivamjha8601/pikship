# TRD — Technical Requirements

> The umbrella for engineering documentation. Two layers within: **HLD** (High-Level Design) and **LLD** (Low-Level Design). Both derive from the PRD.

```
TRD/
├── HLD/   High-Level Design — services, infra, physical data model
└── LLD/   Low-Level Design — per-component schemas, APIs, algorithms
```

## Why two layers, not one combined TRD?

- **HLD** answers *"what services exist and how do they talk?"* — service decomposition, infra choices, data model in storage form, cross-cutting concerns. Updated quarterly. Audience: every engineer + ops.
- **LLD** answers *"what's the schema and what's the algorithm?"* — per-feature implementation specs, API endpoints, error shapes, idempotency rules. Updated per feature, per release. Audience: the team picking up that feature.

A single combined TRD becomes 200+ pages and is impossible to keep current. Splitting lets each layer evolve at its own cadence.

## How to author HLD and LLD from this PRD

When the time comes:

1. **HLD first.** Read `PRD/00`, `PRD/03-product-architecture/`, all `PRD/05-cross-cutting/`. Author the HLD top-down: overview → services → data model → cross-cutting.
2. **LLD per feature.** When eng picks up Feature N, read `PRD/04-features/N-*.md`, write `LLD/features/N-*/` with the implementation spec.
3. **Decision Records** as you go. Capture every "why we chose X over Y" in `HLD/06-decisions/`.

Both are placeholders right now — no content authored yet. See sub-folder READMEs for the outline.

## Status

- HLD: ⏳ not started
- LLD: ⏳ not started
