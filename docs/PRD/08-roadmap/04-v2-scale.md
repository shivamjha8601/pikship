# v2 — Scale & channel breadth + public API

> Goal: 250k+ shipments/month; 5+ channels integrated; mobile app shipped; public API productized.

## What v2 adds

### Channels
- **Flipkart** seller API.
- **Meesho** seller API.
- **Magento (Adobe Commerce)**.
- **BigCommerce** + OpenCart.
- **Dukaan / Bikayi** (social commerce).

### Carriers
- + Rivigo, Trackon, Professional Courier, Spoton, Gati Surface, FirstFlight, ATS, V-Xpress.

### Mobile app
- Native iOS + Android for seller (Owner + Operator) usage.
- Push notifications.
- Bulk operations on mobile.
- Camera capture for weight evidence.

### Public API & developer portal (Feature 21)
- v2 launch — full RESTful API.
- API keys + scopes.
- Webhooks (full event set).
- Idempotency; rate limits.
- Sandbox.
- Node.js, Python, PHP SDKs.
- Developer portal with interactive docs.
- Migration guides (Shiprocket → Pikshipp).

### Insurance
- Partner with Digit / ICICI Lombard / Acko.
- Per-shipment attach (manual + auto-rule).
- Claims workflow productized.

### COD intelligence
- ML-based RTO risk model (v1 was rules).
- COD-to-prepaid conversion (buyer-side prompts).
- Improved buyer COD confirmation conversion.

### NDR intelligence
- 2-way WhatsApp chatbot for NDR.
- AI-assisted action suggestion to seller.

### Reports & analytics
- Custom report builder (drag-drop fields).
- Saved views shareable.
- Scheduled deliveries with Slack/email.
- Real-time dashboards.

### Buyer experience
- Tamil, Telugu, Marathi, Bengali, Gujarati, Kannada localizations.
- Buyer rating and feedback.
- Live driver tracking (where carrier supports).

### Sub-seller advanced
- Independent wallets optional.
- Per-sub-seller config overrides.

### Risk & fraud (Feature 26 — productized)
- ML-based scoring on order intake.
- Cross-seller hashed buyer fraud signals (with privacy guardrails).
- Image-based weight evidence ML.

### Auto-KYC tiering
- Volume-based auto-promotion.
- Continuous KYC (annual GSTIN re-check).

### Performance & reliability
- Throughput: 200 bookings/sec sustained.
- Multi-region option for enterprise.
- 99.95% uptime target.

### Operations
- Scaled support team with KB and macros.
- Bug bounty program live.
- SOC 2 readiness assessment.

## Explicit non-goals at v2

- Hyperlocal (v3).
- B2B / freight (v3).
- ONDC (v3).
- Pikshipp Express (v3, if at all).
- International outbound (v3).
- White-label / reseller (never).

## v2 success metrics

- 250k+ shipments/month.
- 10k+ active sellers.
- 65%+ retention at 365 days.
- NPS > 40.
- API adoption: 20%+ of paid sellers using public API.

## Risks

- Channel adapter quality drift (more channels → more failure modes).
- Mobile app rollout timing (regression risk).
- ML model false positives in COD risk.
- API stability: keep contract stable as we add features.
