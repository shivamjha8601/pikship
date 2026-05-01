# Deferred features

> Things we considered, scoped, and explicitly deferred or removed. Kept here so future contributors don't re-litigate.

## Removed entirely

### White-label / reseller / platform-licensing

**Was:** Feature 20, plus a multi-level tenancy model where Pikshipp would be the root reseller, additional resellers would license the platform under their brand, and run their own sellers.

**Why removed:**
1. Building a *platform-maker* (a thing that builds Shiprockets) is fundamentally harder than building Shiprocket. Smaller market, longer sales cycle.
2. Real white-label requires deep visual customization (CMS-grade) — anything less dissatisfies serious resellers. Logo + color is not white-label.
3. Multi-level tenant trees, config-inheritance engines, per-tenant domain provisioning, per-tenant brand-resolved rendering — significant complexity for every feature in the catalogue.
4. Pikshipp does not need this to compete with Shiprocket. Shiprocket itself is not a platform-maker.

**Replaced by:**
- Per-seller buyer-experience branding in [`04-features/17-buyer-experience.md`](../04-features/17-buyer-experience.md). Seller can configure logo + colors + (optional) custom tracking domain. This covers the legitimate "let my buyers see my brand" need.

**When could this come back?**
- If a credible business case emerges (e.g., a regulated fintech wants to ship under their brand using our infra and is willing to pay for full white-label tier). Treat as a v3+ research question.

## Deferred to a later version

### Public API & webhooks (Feature 21)

**Status:** Deferred from v1 to v2.

**Why deferred:**
- v1 sellers can succeed with a dashboard. API is a productization, not foundational.
- Building a polished public API (developer portal, SDKs in 5 languages, migration guides, sandbox parity) is a meaningful chunk of work.
- Ship internal-only at v1; productize at v2.

**v1 architectural commitment:** internal APIs of every feature are clean and RESTful so v2 is a productization, not a re-architecture.

### Hyperlocal & same-day (Feature 23)

**Status:** v3.

**Why:** Different cadence, different partners, different buyer expectations. Better to nail core aggregation first.

### B2B / freight (Feature 24)

**Status:** v3.

**Why:** Different unit of work (consignment, not parcel). Different commercial terms. v3 expansion.

### ONDC channel

**Status:** v3.

**Why:** Worth following but not strategic for v0/v1/v2. Will integrate as a channel in v3.

### Native mobile app

**Status:** v2.

**Why:** Mobile-web suffices at v1; native is a productization for scaled ops.

### First-party fleet (Pikshipp Express)

**Status:** v3+ — and only if (a) v1+v2 prove successful, (b) a specific seller demand the aggregator can't serve emerges.

**Architecture commitment:** the carrier adapter framework treats first-party as just another carrier. No fork required.

### Working capital / credit-against-COD products

**Status:** v3+, partner-led (regulated).

**Why:** Adjacent business; not core; regulated.

### International outbound

**Status:** v3+.

**Why:** India is the prize. International is an extension once home market is locked.

## Architectural decisions made (won't change without a re-litigation)

### Single-aggregator model
- Pikshipp is the only operator on this platform.
- No tenant tree.
- Sellers are isolated via seller_id at the data layer.
- See [`03-product-architecture/02-multi-tenancy-model.md`](../03-product-architecture/02-multi-tenancy-model.md).

### Configurability via policy engine
- Plans are bundles of config values; not feature gates.
- Adding new behavior axes is config, not code.
- See [`03-product-architecture/05-policy-engine.md`](../03-product-architecture/05-policy-engine.md).

### Audit-everything cross-cutting
- Every privileged action audit-logged.
- Tamper-evident for high-value events.
- See [`05-cross-cutting/06-audit-and-change-log.md`](../05-cross-cutting/06-audit-and-change-log.md).

### Allocation as a first-class concept
- Auditable explanations.
- Multi-objective scoring.
- Pricing engine is a subsystem.
- See [`04-features/25-allocation-engine.md`](../04-features/25-allocation-engine.md).

### Pooled support; no dedicated CSM
- All sellers share Pikshipp Support pool.
- SLA tier varies by plan, not by assigned-human.

### KYC as risk tool, not compliance ritual
- Depth scales with risk and volume.
- See [`04-features/01-identity-and-onboarding.md`](../04-features/01-identity-and-onboarding.md).

### No notification tier
- Every seller gets WhatsApp + SMS + email per shipment as standard.
- See [`04-features/16-notifications.md`](../04-features/16-notifications.md).
