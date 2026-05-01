# Security & compliance

> Cross-cutting. Applies to every feature. Where a feature has feature-specific concerns, they live in the feature doc; the platform-wide invariants live here.

## Goals

- Tenant data isolation that survives bugs.
- Compliance with India-specific regimes: GST, IT Act, DPDP-Act 2023, RBI PPI rules (where applicable), telecom DLT for SMS, Meta WhatsApp policy, IRDAI for insurance intermediation.
- Security posture that is "we believe a determined attacker fails" — not "we hope".

## Non-goals

- ISO/SOC certifications in v1 (target v2/v3 if enterprise/white-label demand).
- HIPAA / PCI-DSS — we don't store buyer card data; PG handles.

## Threat model (high level)

| Threat | Surface | Severity |
|---|---|---|
| Cross-tenant data leak | Every endpoint, every query | Critical (existential for white-label) |
| Wallet fraud (recharge bypass, replay) | Wallet, PG webhooks | Critical |
| API key theft | API surface | High |
| Webhook signing bypass (carrier side) | Webhook receivers | High |
| Buyer PII exfiltration (scraping tracking pages) | Tracking endpoints | High |
| KYC document leak | KYC service | High |
| Internal staff abuse | Admin console | High (audited) |
| Carrier credential leak | Adapter config | High |
| Account takeover (seller) | Auth | High |
| DDoS on tracking domain | Edge | Medium |
| Supply chain (malicious dependency) | Build | Medium |

## Hard rules

1. **Tenant scoping at the data layer**, not application layer. Every persistent table has tenant id columns; queries enforced via row-level security or query interceptor. No "raw table" access in code.
2. **Wallet integrity invariants**: ledger sum == balance; double-entry; idempotent posting. Daily checks; alert on first mismatch.
3. **Secrets in vault** (HashiCorp Vault / AWS Secrets Manager). Never in code, repo, env files. Rotation supported.
4. **HMAC-signed webhooks both inbound and outbound** with rotated secrets.
5. **TLS 1.2+** everywhere; mTLS for service-to-service where applicable.
6. **Audit log** for every privileged operation (admin console, ops, finance, impersonation).
7. **PII minimization** — collect what's required by carriers/GST; mask elsewhere.
8. **Buyer data retention** — purge tracking tokens N days post-delivery; keep analytics aggregates only.
9. **No direct DB access in production** by humans except break-glass (audited).
10. **Penetration testing** quarterly; bug bounty by v2.

## Auth & identity

- Auth provider: identity service (Feature 01) + secure session management.
- MFA available; required for high-privilege roles.
- Brute-force protection on login.
- API keys:
  - Hashed at rest (we never see plaintext after creation).
  - Scoped permissions.
  - Anomaly detection (sudden geographic / volume change).
- SSO (SAML/OIDC) for enterprise + reseller staff (v2).

## Authorization model

- RBAC v1; ABAC v2.
- Authorization checks at every domain operation, not only at API gateway.
- Tenant scope is part of the authorization, not a separate filter.
- Defense in depth: multiple checks for sensitive operations.

## Encryption

- **At rest**: storage volumes encrypted (AES-256). Object storage encrypted. Document store encrypted. Secrets in vault.
- **In transit**: TLS 1.2+; HSTS; certificate pinning where applicable.
- **Application-level encryption** for highly sensitive fields (Aadhaar reference, bank account number) — separate KMS-managed keys.

## Logging & audit

- Audit log: append-only; tamper-evident (hash chains).
- Categories:
  - Auth events.
  - Privileged ops actions.
  - Tenant lifecycle changes.
  - Wallet operations above threshold.
  - Cross-tenant access (Pikshipp/reseller staff "view as").
  - KYC decisions.
  - API key creation/revocation.
- Retention: 7 years (DPDP / IT Act guidance for financial).
- Immutable export per tenant on request.

## Compliance regimes

### DPDP Act 2023 (India)

- Lawful processing basis for buyer data: contractual necessity (delivery).
- Buyer consent for marketing/promotional comms; transactional shipping comms exempt under contractual basis.
- Data principal rights: access, correction, erasure (delete), grievance.
- Data fiduciary: Pikshipp / reseller jointly per contractual flow.
- Data Protection Officer (DPO) appointed.
- Breach notification: 72h to Data Protection Board.
- Data retention only as long as needed; clear deletion policies per category.

### IT Act 2000

- Reasonable security practices (Section 43A): we follow best practices; documented.
- Breach reporting (CERT-In within 6h of detection).

### GST Act

- E-invoicing for supplies above threshold (handled in Feature 13).
- E-way bill (Feature 24).
- HSN/SAC compliance.

### RBI PPI rules (if we operate wallet float)

- Wallet model — semi-closed PPI? Or covered as "bookings deposit" not PPI? Legal review required.
- Float exposure limits.
- KYC for wallet holders.

### Telecom Regulatory Authority (TRAI) — DLT for SMS

- All transactional templates registered.
- Buyer comms via DLT-registered sender IDs.

### Meta WhatsApp Business Policy

- Templates approved.
- Opt-in basis required for marketing; transactional permitted.
- Spam thresholds tracked.

### IRDAI (Insurance Regulatory)

- Pikshipp acts as intermediary (referrer or corporate agent — model TBD).
- Insurance partner holds underwriting risk.

## Operational security

- Production access restricted; break-glass with audit + approval.
- Bastion hosts; SSH key rotation.
- Vulnerability scanning of dependencies in CI.
- Container image scanning.
- WAF on edge.
- DDoS protection (CloudFlare / AWS Shield).
- Rate limiting at edge + per-API-key + per-IP.

## Incident response

- Severity definitions (P0–P3).
- On-call rotation; PagerDuty/Slack escalation.
- Postmortems published internally; redacted for tenants on request.
- Customer notification per regulatory and contractual obligations.

## Vendor risk

- Each third-party (carrier, PG, KYC, comms, insurer, IPaaS) has:
  - Data processing agreement.
  - Security review.
  - Periodic re-evaluation.
- No production data leaves our perimeter except as contractually required.

## Open questions

- **Q-SC1** — RBI PPI applicability — wallet model classification. Default: legal review pre-launch.
- **Q-SC2** — Insurance intermediary model — referrer vs corporate agent. Default: referrer v1.
- **Q-SC3** — DPO joint with reseller for white-label (we are processor / fiduciary depending on data flow). Default: per-contract.
- **Q-SC4** — Bug bounty program timing. Default: v2.
- **Q-SC5** — SOC 2 Type II target. Default: end of year 2.
