# KYC & verification

> Identity / business verification vendors. Pairs with `04-features/01-identity-and-onboarding.md`.

## Vendors

| Vendor | Strengths | Use cases |
|---|---|---|
| **Karza Technologies** | Broad coverage; established | GSTIN, PAN, Aadhaar (via DigiLocker), bank, MSME |
| **Hyperverge** | Strong OCR + face match | Document OCR, face liveness |
| **Signzy** | Workflow-oriented; banking-grade | Full KYC workflows |
| **IDfy** | Established; many APIs | Comprehensive |
| **DigiLocker** (govt) | Authoritative | User-pulled docs |

**Multi-vendor** posture: Karza primary; Hyperverge for OCR/face; manual fallback.

## Verifications used

| Check | Purpose |
|---|---|
| GSTIN | Business legal status; legal name pull |
| PAN (individual / entity) | Tax identity |
| Aadhaar (masked) | Personal identity (proprietorship) |
| Bank account (penny-drop) | COD remittance target |
| MSME / Udyam | Tier benefits |
| Business address (later) | Pickup proof v2 |

## API patterns

- REST with API key.
- Async: some checks (especially address) async with webhook.
- Cost: ₹5–25 per check; budget per-application.

## Failover

- Primary fail → secondary vendor (where available).
- All-vendor fail → manual ops review.

## Storage of evidence

- KYC documents in encrypted object storage; KMS-managed keys.
- Access logs.
- Retention: 7 years (financial) post-active; purged after de-active per legal.

## DPDP-Act handling

- Consent recorded.
- Purpose-limited (no marketing use).
- Data principal rights honored (access / erase requests).

## Open questions

- **Q-KYC1** — DigiLocker integration as primary user-pull mode? Better authority but UX friction. Default: backup v1.
- **Q-KYC2** — Aadhaar policy: store hashed reference or never store? Default: hashed reference; mask elsewhere.
- **Q-KYC3** — Video KYC for high-value sellers? Vendor + manual ops; v3.
- **Q-KYC4** — Continuous KYC (re-check periodically)? Default: yes for GSTIN annually.
