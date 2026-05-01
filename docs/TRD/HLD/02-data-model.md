# Data model — physical schema overview

> Inventory of every table, organized by domain. SQL details (DDL, indexes, constraints) live in LLD per domain. This doc is the **map** of what lives where.

## Conventions (recap)

- All PKs `UUID` via `gen_random_uuid()`.
- Tenancy column `seller_id UUID NOT NULL` on every seller-scoped table.
- `created_at TIMESTAMPTZ` on every table; `updated_at` on mutable tables.
- Tables prefixed by domain.
- RLS on every seller-scoped table; the `pikshipp_app` role enforces it.
- Money in paise (int64), DB type `BIGINT`.

## Inventory by domain

### Identity & sessions
| Table | Purpose | Tenancy |
|---|---|---|
| `app_user` | Pikshipp users (seller staff + ops) | none (joined) |
| `oauth_link` | Google OAuth provider links | by user |
| `session` | Server-side sessions: `(token_hash, user_id, seller_id, expires_at, revoked_at)` | by user |
| `seller_user` | Join: which user belongs to which seller, with roles | by seller |

### Seller organization
| Table | Purpose | Tenancy |
|---|---|---|
| `seller` | Seller orgs | self |
| `sub_seller` | Sub-sellers (branches) | by parent seller |
| `seller_audit_event` | Per-seller audit (lifecycle changes) | by seller |
| `kyc_application` | KYC applications and review state | by seller |
| `kyc_document` | Uploaded documents (S3 ref) | by seller |

### Policy engine (cross-cutting)
| Table | Purpose | Tenancy |
|---|---|---|
| `policy_setting_definition` | Schema of all valid keys (seeded from Go code) | global |
| `policy_seller_override` | Per-seller value overrides | by seller |
| `policy_lock` | Pikshipp-pinned values (cannot be overridden) | global / by seller-type |

### Audit (cross-cutting)
| Table | Purpose | Tenancy |
|---|---|---|
| `audit_event` | Per-seller hash-chained audit log | by seller |
| `operator_action_audit` | Cross-seller ops actions (separate chain) | platform |

### Idempotency (cross-cutting)
| Table | Purpose | Tenancy |
|---|---|---|
| `idempotency_key` | (seller_id, key, response, created_at) | by seller |

### Outbox (cross-cutting)
| Table | Purpose | Tenancy |
|---|---|---|
| `outbox_event` | Pending domain events | by seller (denormalized) |

### River queue (managed by river library)
Tables: `river_job`, `river_leader`, `river_migration`. **Managed by river itself**; not in our schema directly.

### Channels
| Table | Purpose | Tenancy |
|---|---|---|
| `channel` | Per-seller channel connections | by seller |
| `channel_event` | Inbound webhook events (idempotent) | by seller |
| `channel_credential` | Encrypted secrets (plaintext at v0; SSM at v1) | by seller |

### Catalog & pickup
| Table | Purpose | Tenancy |
|---|---|---|
| `pickup_location` | Seller's pickup addresses | by seller |
| `pickup_carrier_registration` | Per-(pickup, carrier) registration status | by seller |
| `holiday_calendar_entry` | Pickup-location holiday overrides | by seller |
| `product` | SKU master | by seller |

### Orders
| Table | Purpose | Tenancy |
|---|---|---|
| `order` | Canonical orders | by seller |
| `order_line_item` | Line items per order | by seller |
| `order_validation_result` | Validation outputs (block/warn/auto-fix) | by seller |
| `routing_rule` | Per-seller auto-routing rules | by seller |
| `bulk_job` | Bulk operation tracking (book, label, etc.) | by seller |

### Carriers
| Table | Purpose | Tenancy |
|---|---|---|
| `carrier` | Global carrier config | platform |
| `carrier_credential` | Per-environment credentials | platform |
| `carrier_serviceability` | Per-(carrier, pincode, service) coverage | platform |
| `carrier_health_state` | Circuit-breaker state per carrier | platform |
| `carrier_event` | Inbound webhook events from carriers | platform (with seller_id denormalized for query) |

### Pricing
| Table | Purpose | Tenancy |
|---|---|---|
| `rate_card` | Versioned rate cards | by scope (pikshipp / seller_type / seller) |
| `rate_card_zone` | Per-card zone definitions (pincode pattern → zone code) | by card |
| `rate_card_slab` | Per-(card, zone, weight, payment_mode) base rates | by card |
| `rate_card_adjustment` | Surcharges (fuel, COD, ODA, peak, promo) | by card |

### Allocation
| Table | Purpose | Tenancy |
|---|---|---|
| `carrier_reliability` | Per-(carrier, pincode_zone) precomputed scores | platform (read by all sellers) |
| `allocation_decision` | Persisted decision per booking with full audit | by seller |

### Shipments
| Table | Purpose | Tenancy |
|---|---|---|
| `shipment` | Canonical shipments | by seller |
| `shipment_attempt` | Booking attempts (retry log) | by seller |
| `manifest` | Per-(carrier, pickup, date) manifests | by seller |
| `manifest_shipment` | Join: shipments in a manifest | by seller |
| `label` | Generated labels (S3 ref) | by seller |

### Tracking
| Table | Purpose | Tenancy |
|---|---|---|
| `tracking_event` | Append-only events | by seller (denormalized) |
| `shipment_status_history` | Canonical status transitions | by seller |

### NDR
| Table | Purpose | Tenancy |
|---|---|---|
| `ndr_event` | NDR occurrences per shipment | by seller |
| `ndr_action` | Actions taken on NDRs | by seller |
| `ndr_outreach` | Buyer outreach attempts | by seller |
| `ndr_rule` | Per-seller auto-rules | by seller |

### RTO & returns
| Table | Purpose | Tenancy |
|---|---|---|
| `rto_record` | RTO lifecycle | by seller |
| `return_request` | Buyer-initiated returns | by seller |
| `qc_outcome` | Receipt QC results | by seller |
| `reverse_shipment` | Reverse pickup shipment record | by seller |

### COD
| Table | Purpose | Tenancy |
|---|---|---|
| `cod_verification` | Pre-pickup buyer confirms | by seller |
| `cod_remittance` | Per-shipment remittance ledger | by seller |
| `cod_remittance_file` | Carrier remittance file ingestions | platform |

### Wallet & billing
| Table | Purpose | Tenancy |
|---|---|---|
| `wallet_account` | Per-seller wallet (cached balance) | by seller |
| `wallet_ledger_entry` | Append-only ledger | by seller |
| `wallet_hold` | Two-phase reservations | by seller |
| `wallet_idempotency_key` | Wallet-specific idempotency | by seller |
| `wallet_invariant_check_result` | Daily invariant check log | by seller |
| `recharge_event` | PG webhook ingestion | by seller |
| `invoice` | Monthly tax invoices | by seller |

### Weight reconciliation
| Table | Purpose | Tenancy |
|---|---|---|
| `weight_dispute` | Per-shipment dispute state | by seller |
| `weight_evidence` | Photo/scale evidence (S3 refs) | by seller |
| `carrier_reweigh_file` | Carrier-supplied reweigh data ingestion | platform |

### Reports / analytics
| Table | Purpose | Tenancy |
|---|---|---|
| `agg_daily_seller_shipments` | Pre-aggregated facts | by seller |
| `agg_daily_carrier_performance` | Per-carrier rolled metrics | platform |
| `agg_daily_pincode_outcomes` | Per-pincode metrics | platform |

(Aggregations populated by daily river jobs; reports module reads them via `pikshipp_reports` BYPASSRLS role.)

### Notifications
| Table | Purpose | Tenancy |
|---|---|---|
| `notification` | Per-send record | by seller |
| `notification_template` | Templates (Pikshipp default + seller overrides) | by scope |
| `notification_opt_out` | Per-seller, per-recipient opt-outs | by seller |

### Buyer experience
| Table | Purpose | Tenancy |
|---|---|---|
| `buyer_session` | Ephemeral buyer-page sessions | by seller |
| `buyer_feedback` | Post-delivery ratings/comments | by seller |
| `custom_domain` | Per-seller branded tracking domains | by seller |

### Support & ticketing
| Table | Purpose | Tenancy |
|---|---|---|
| `ticket` | Tickets | by seller |
| `ticket_message` | Conversation history | by seller |
| `knowledge_article` | KB articles | by scope |

### Risk & fraud
| Table | Purpose | Tenancy |
|---|---|---|
| `risk_score` | Order/seller/buyer-phone scores | by seller |
| `risk_action` | Actions taken on signals | by seller |
| `fraud_pattern` | Detected pattern records | by seller (or platform) |

### Contracts
| Table | Purpose | Tenancy |
|---|---|---|
| `contract` | Signed contracts + machine-readable terms | by seller |
| `contract_amendment` | Amendments | by seller |
| `document` | Generic document storage refs | by scope |

## Total table count

Approximately **70 tables** at v0–v1 across all domains. Manageable. SQL DDL lives per-domain in LLD.

## Migration ordering

Migrations are numbered sequentially. Domain modules can be added in any order, but with dependencies:
1. Cross-cutting: `app_user`, `seller`, `policy_*`, `audit_*`, `idempotency_*`, `outbox_*`.
2. Channels + carriers + catalog (independent).
3. Orders (depends on seller, catalog, channels).
4. Pricing + allocation (depend on carriers).
5. Shipments + tracking (depend on orders + carriers + allocation).
6. NDR + RTO + COD (depend on shipments + tracking).
7. Wallet (depends on seller).
8. Reconciliation (depends on shipments + wallet + carriers).
9. Reports (independent; reads everywhere).
10. Notifications + buyerexp + support + risk + contracts (mostly independent or late).

## Things deliberately NOT in DB

- File contents (labels, photos, raw payloads): S3, ref-only in DB.
- Secrets at v0: env vars (revisit).
- Cache state: in-process or via LISTEN/NOTIFY signals.
- River internal state: river's own tables, but functionally an opaque queue.

## What this doc does not contain

- DDL with column types — see LLD per domain.
- Index lists — see LLD per domain (a few are mentioned in the architecture docs as load-bearing).
- Per-table query patterns — see LLD.
- Migration scripts — see `migrations/` in the codebase, when authored.
