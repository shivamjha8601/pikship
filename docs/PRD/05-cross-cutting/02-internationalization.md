# Internationalization (i18n) & localization (l10n)

## Scope

India is one country with 22 official languages and 28 states with regional addressing/format variation. Even though we are India-only, we need real i18n machinery from day 1. International expansion (FedEx/DHL outbound) is out of scope until v3+; this doc focuses on **Indian multilingual** primarily.

## Goals

- Seller dashboard in English at v1; Hindi UI at v2.
- Buyer-facing pages in English + Hindi at v1; Tamil, Telugu, Marathi, Bengali, Gujarati, Kannada at v2.
- All notification templates support per-locale variants.
- Address parsing and pincode validation locale-aware.
- Currency: INR only v1; multi-currency considered v3.

## Design principles

- **Locale = language + region**, e.g., `en-IN`, `hi-IN`, `ta-IN`.
- **No hard-coded strings** in code; all UI strings via i18n keys.
- **Dates and numbers** formatted per locale (Indian numbering: lakh/crore).
- **Pluralization rules** per locale (Hindi has different plural rules from English).
- **Bidirectional support** not currently required (Hindi/regional are LTR); architecture supports for future Arabic/Hebrew.

## Locale resolution

- Seller user: configured in profile.
- Buyer: detected from notification context (e.g., outreach in Hindi → page in Hindi); query param override; user-toggle.
- Falls back to `en-IN` if locale missing.

## Translation pipeline

- Source strings in English.
- Translation keys structured (`shipment.tracking.steps.picked_up`).
- Translation memory tooling (e.g., Crowdin / Lokalise).
- Reviewed by native speakers before release.
- Per-tenant overrides allowed (resellers may rephrase).

## Currency & numbers

- INR only at v1.
- Indian numbering format (1,00,000 vs 100,000) where it's the user's preference; configurable.
- Decimal handling consistent: paise (minor units) for storage, INR for display.

## Address handling

- Pincode = 6 digits, anchor identifier.
- State enumerations per ISO 3166-2:IN.
- City names variable spellings tolerated; pincode is authoritative.
- Regional alphabets in addresses: stored as Unicode; not transliterated automatically (carriers may require Latin).

## Open questions

- **Q-I18N1** — Per-channel locale heuristic (Meesho often regional vs Shopify often English) — auto-detect or default? Default: auto-detect with confidence threshold.
- **Q-I18N2** — Carrier label language: most carriers require English; we keep English on labels regardless of buyer locale.
- **Q-I18N3** — Right-to-left support (Urdu) — defer to v3.
