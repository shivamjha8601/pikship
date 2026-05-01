# Cross-cutting: External dependencies & failure matrix

> Every external system we depend on; its purpose; what happens when it's down; how we recover. One page.

## v0 dependencies

| System | Purpose | Critical to v0? | Latency target | Health check |
|---|---|---|---|---|
| AWS RDS Postgres | All persistent state | Yes — full outage if down | < 5ms p50 | `/readyz` pings |
| AWS S3 | Labels, raw payloads, KYC docs | Yes — degrades booking | < 50ms p50 | `/readyz` HEAD on probe object (60s cache) |
| Google OAuth | Login | Yes — no new logins if down (existing sessions OK) | n/a | n/a |
| MSG91 | SMS / OTP | Yes — no phone verify; OTP login flows blocked (we don't have OTP login at v0) | < 2s p50 | none synchronous |
| AWS SES | Transactional email | No — degraded but operational | < 2s p50 | none synchronous |
| Razorpay | Wallet recharge PG | No — recharge unavailable; existing balance unaffected | < 3s p50 | none synchronous |
| Delhivery API | The only carrier at v0 | Yes — no new bookings; tracking degraded | < 3s p95 | circuit breaker |
| Shopify API | Channel ingest (per seller) | No per-seller — affects only that seller | < 2s p50 | per-channel auth check |
| CloudWatch Logs | Log shipping | No — Vector buffers locally | n/a | Vector self-monitor |

## Failure matrix

For each dep, the question: **what does the seller experience when this is down?**

### AWS RDS Postgres
- **Symptom**: All HTTP requests fail with 503; `/readyz` returns 503; ALB de-routes (multi-instance only — at N=1 it's a full outage).
- **Recovery**: Multi-AZ failover (RTO ~30s, RPO ~5min).
- **Mitigation**: Multi-AZ from day 0 (ADR 0008); automated backup retention; quarterly restore drill.
- **Seller-visible**: ~30 seconds of "Service unavailable, retry" during failover.
- **Action during outage**: nothing required from seller; ops monitors.

### AWS S3
- **Symptom**: Booking succeeds but label generation fails (`label_url` blank); existing tracking pages with cached labels work; new uploads fail.
- **Recovery**: AWS-side; multi-AZ in region by default.
- **Mitigation**: Retries with backoff; `/readyz` includes S3 reachability so single-instance failure routes traffic away (multi-instance future); persisted DB references; raw payloads can be re-fetched from carrier on demand.
- **Seller-visible**: "Label not yet ready, retry shortly" on booking success page.
- **Action during outage**: ops can manually invoke a regeneration job once S3 recovers.

### Google OAuth
- **Symptom**: New logins fail. Existing sessions continue working until expiry (24h since last activity).
- **Recovery**: Google-side.
- **Mitigation**: Session lifetime is long enough that brief OAuth outages don't affect active sellers.
- **Seller-visible**: "Login temporarily unavailable" on the OAuth callback page; existing sessions unaffected.
- **Action during outage**: nothing.

### MSG91 (SMS)
- **Symptom**: Phone verification flows fail; NDR buyer outreach via SMS fails; COD verification SMS fails.
- **Recovery**: MSG91-side; we have no automatic fallback to a secondary vendor at v0 (Twilio integration is v1).
- **Mitigation**: Outbound SMS jobs retry with backoff (up to 5 attempts over ~1h); after that, dead-letter and alert.
- **Seller-visible**: Phone verification stalls; NDR outreach delayed.
- **Action during outage**: ops monitors; communicate to friendly sellers if extended.

### AWS SES (email)
- **Symptom**: Transactional emails not delivered.
- **Recovery**: AWS-side.
- **Mitigation**: Outbound email jobs retry; for KYC outcome emails, ops can resend manually.
- **Seller-visible**: "Email may be delayed" — usually invisible.
- **Action during outage**: nothing immediate.

### Razorpay
- **Symptom**: Wallet recharge UI shows "Payment unavailable, try again or use NEFT".
- **Recovery**: Razorpay-side.
- **Mitigation**: Manual NEFT path documented; ops can credit wallet manually with audit.
- **Seller-visible**: Cannot top up wallet; existing balance and bookings unaffected.
- **Action during outage**: ops can manually reconcile NEFT-paid recharges.

### Delhivery API
- **Symptom**: Booking fails with `carrier_unavailable`; tracking polling errors; circuit breaker trips.
- **Recovery**: Delhivery-side.
- **Mitigation**: Circuit breaker (ADR 0007); allocation engine routes around (no alternative carrier at v0 — booking just fails); reconcile cron handles in-flight `pending_carrier` shipments; tracking webhooks accepted but processing pauses if downstream is also affected.
- **Seller-visible**: New bookings rejected with clear message; in-flight shipments not visibly affected (status updates may lag).
- **Action during outage**: ops engages Delhivery support; seller comms templated; degraded operation badge in dashboard.

### Shopify API
- **Symptom**: Per-seller; ingestion fails for that seller's channel.
- **Recovery**: Shopify-side, or in case of expired tokens, seller-side reconnect.
- **Mitigation**: Polling fallback when webhooks lag; auth-expired detection emits `channel.auth_expired` and notifies seller.
- **Seller-visible**: Channel health card shows red; seller reconnects when ready.
- **Action during outage**: nothing required from us; per-seller issue.

### CloudWatch Logs (via Vector)
- **Symptom**: Logs not appearing in CloudWatch.
- **Recovery**: AWS-side.
- **Mitigation**: Vector buffers on disk; logs replay when reachable.
- **Seller-visible**: None; internal only.
- **Action during outage**: monitor Vector buffer disk usage; noisy alert only if buffer fills.

## Dependency degradation matrix

Quick lookup: which seller-facing capabilities work when which dep is down?

|  | RDS down | S3 down | Google OAuth down | MSG91 down | Razorpay down | Delhivery down | Shopify down |
|---|---|---|---|---|---|---|---|
| Login (existing session) | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| New login | ❌ | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ |
| View dashboard | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Pull new orders (Shopify) | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ |
| Manual order entry | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Book shipment | ❌ | ⚠️ (label may delay) | ✅ | ✅ | ✅ | ❌ | ✅ |
| Generate label | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ |
| View tracking | ❌ | ✅ | ✅ | ✅ | ✅ | ⚠️ (lag) | ✅ |
| Recharge wallet | ❌ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ |
| Phone verify | ❌ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ |
| Buyer reschedule (NDR) | ❌ | ✅ | ✅ | ⚠️ (no SMS reach) | ✅ | ⚠️ (action-fail) | ✅ |

## Vendor agreements & escalation contacts

(Maintained in `IMPL/runbooks/vendor-contacts.md` — not in version control for security.)

Each vendor has:
- 24/7 support contact (or office hours).
- Escalation tree for P0/P1.
- Status page URL.
- SLA commitment (where applicable).

## Adding a new external dependency

When introducing a new vendor:
1. Document here: purpose, criticality, latency target, health check.
2. Add to dependency degradation matrix.
3. Add ADR if non-obvious.
4. Implement circuit breaker if synchronous-call dependency.
5. Configure timeouts (default: 5s for HTTP).
6. Add monitoring + alerting.
7. Add a runbook section for outages.

## Vendor lock-in considerations

Where we abstract behind interfaces (`auth.Authenticator`, `carriers.Adapter`, `channels.Adapter`, `notifications.Sender`), swapping vendors is a configuration + new implementation; not a refactor. Where we don't abstract (e.g., direct AWS SDK use for S3), we accept the lock-in for simplicity.
