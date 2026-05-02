// Package audit provides an append-only, hash-chained event log.
//
// High-value events (financial mutations, KYC decisions, ops privileged
// actions, contract changes) emit synchronously inside the originating DB
// transaction — guaranteeing audit-or-rollback. Lower-value events emit
// async via the outbox.
//
// Per-seller hash chains let us export a verifiable history; the platform
// chain (seller_id IS NULL) covers cross-seller ops actions.
//
// Per LLD §03-services/02-audit. Migrations 0004 created the underlying
// tables (audit_event, operator_action_audit).
package audit
