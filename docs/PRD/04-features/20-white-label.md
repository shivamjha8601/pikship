# Feature 20 — *(removed)*

> Slot reserved. Was previously "White-label & branding".

## Status

**Removed from product scope.** Pikshipp is a single-aggregator platform — there is no white-label / reseller / platform-licensing offering. See:

- [`01-vision-and-strategy/01-vision-and-mission.md`](../01-vision-and-strategy/01-vision-and-mission.md) — explicit anti-vision.
- [`03-product-architecture/02-multi-tenancy-model.md`](../03-product-architecture/02-multi-tenancy-model.md) — single-aggregator model.
- [`09-appendix/04-deferred-features.md`](../09-appendix/04-deferred-features.md) — historical record of why removed.

## What replaced it

Per-seller buyer-experience branding (logo, colors, optional custom domain) is fully covered in [`17-buyer-experience.md`](./17-buyer-experience.md). That captures the legitimate "let my buyers see *my* brand" need without the operational and architectural complexity of a true white-label tenant model.

## Number kept stable

The feature number `20` is retained as a placeholder so cross-references in older documents don't shift. Future features should pick numbers ≥ 28.
