# Performance & reliability

## SLOs (service level objectives)

| Surface | Metric | Target |
|---|---|---|
| Seller dashboard | Page load (initial) | < 3 s P95 |
| Seller dashboard | API GET P95 | < 500 ms |
| Booking | API POST P95 (carrier permitting) | < 2 s |
| Rate quote | API GET P95 | < 500 ms |
| Tracking event ingest → visible | P95 | < 5 min |
| Buyer tracking page | TTFB on 3G | < 1.5 s |
| Webhook outbound delivery | P95 | < 30 s |
| Wallet recharge reflection | P95 | < 60 s |
| Platform availability | Booking + tracking endpoints | 99.9% |
| Platform availability | Background jobs | 99.5% |
| Public API | Aggregated availability | 99.9% |

## Throughput targets

| Operation | v1 | v2 | v3 |
|---|---|---|---|
| Bookings/sec sustained | 50 | 200 | 1000 |
| Rate quotes/sec | 200 | 1000 | 5000 |
| Tracking events/sec | 500 | 2000 | 10,000 |
| Concurrent users | 5,000 | 30,000 | 200,000 |

## Reliability patterns

- **Idempotent writes**: every public POST.
- **Two-phase wallet** for booking integrity.
- **Carrier circuit breakers**: per-carrier; auto-trip on error rate; auto-recover.
- **Backpressure** on ingestion queues; alerts on growth.
- **Replay** capability for failed ingestion events.
- **Daily reconciliation** of: ledger ↔ wallet, COD ↔ remittance, tracking events ↔ shipment status.

## Disaster recovery

- RPO (Recovery Point Objective): 5 min for transactional; 1 h for analytics.
- RTO (Recovery Time Objective): 30 min for booking critical path; 2 h full platform.
- Cross-region active-passive (or active-active where economical).
- Backups: continuous + daily snapshot; 30-day retention.
- Restore drills quarterly.

## Capacity planning

- Each scale tier (v1 / v2 / v3) has a quarterly capacity review.
- Headroom 30%+ at peak.
- Carrier API rate limits modeled; degradation modes documented.

## Caching

- Read-heavy endpoints (rate quotes, serviceability, tracking page) cached at edge or app layer.
- Cache keys include tenant id.
- Cache invalidation on relevant updates.

## Carrier-side dependency

We are bounded by carrier SLA; we surface carrier health to sellers. Our SLA = our infra SLA + transparent carrier SLA pass-through.

## Performance budgets per page

- Buyer tracking page: HTML+CSS shell < 25 KB; JS < 50 KB; images optimized.
- Seller dashboard initial bundle: < 250 KB gzipped; lazy-loaded routes.
- Reports page: pre-aggregated; no full-table scans on hot path.

## Database performance

- Read replicas for reporting workloads.
- Write paths optimized; analytical queries on warehouse, not OLTP.
- Indexing reviewed per release.

## Open questions

- **Q-PR1** — Multi-region deployment timing. Default: v2 if enterprise demand, else single-region (Mumbai) v1.
- **Q-PR2** — RPO/RTO tightening — depends on enterprise customer commitments.
- **Q-PR3** — Real-time vs batch tracking event processing — default real-time.
