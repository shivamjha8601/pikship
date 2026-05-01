# ADR 0010 — Secrets in env vars at v0 (technical debt)

Date: 2026-05-01
Status: Accepted (with explicit revisit-before-prod gate)
Owner: Architect A

## Context

The user explicitly asked for v0 to keep secrets in plain config / env vars: "everything in config, i know not ideal, we will change before we move to prod, but no secrets for now."

This is reasonable for v0 with internal-only / friendly sellers. It is **not** acceptable for prod with real customers and real money.

## Decision

**At v0, all secrets are stored in environment variables loaded at process start from `/etc/pikshipp.env`** (mode 600, owned by `pikshipp` user). No KMS, no Secrets Manager, no SSM Parameter Store.

The application configuration loader is structured so that switching to a different secret source is a pure infrastructure change.

## Alternatives considered

### SSM Parameter Store from day 0
- Rejected for v0 per user direction. Will adopt before prod (per v1 readiness).

### AWS Secrets Manager from day 0
- More expensive than SSM. Not warranted at v0.

### HashiCorp Vault
- Operationally heavy for a single-developer deployment.

### Plaintext env vars (chosen for v0)
- Acceptable as explicit technical debt with a tracked revisit gate.

## Consequences

### What this commits us to
- A v1 readiness gate: "secrets in SSM" must be done before any non-friendly seller is onboarded.
- Tracking: this ADR is referenced in the v1 launch checklist.
- Acknowledgment in the tenets ([`00-tenets.md`](../00-tenets.md) §10.3).

### What it costs
- A real risk: secrets in `/etc/pikshipp.env` are accessible to anyone with root on the EC2 instance. Mitigated by IAM access controls + bastion-only SSH.
- No automated rotation. Manual updates require ops intervention + service restart.

### What it enables
- Faster v0 launch. Less plumbing to set up.
- Allows the user to see the application running end-to-end before deciding on secrets infra.

## Migration path (before v1 / non-friendly sellers)

1. Provision SSM Parameter Store hierarchy: `/pikshipp/<env>/<key>`.
2. Update config loader to read from SSM via AWS SDK at startup (with env-var fallback for local dev).
3. Migrate secrets one by one; verify each.
4. Remove `/etc/pikshipp.env` from production.
5. Update IAM role for the EC2 instance to grant SSM read.
6. Update CI to never log secrets (already enforced).
7. Document rotation procedure in a runbook.

## v0 mitigations

- `/etc/pikshipp.env` is mode 600, owned by `pikshipp:pikshipp`.
- EC2 instance is in private subnet; SSH only via bastion.
- Vector / journald log filters scrub env-var-shaped strings from logs.
- Secrets are not committed to repo; not in CI logs.

## Open questions

- KMS encryption of the `pikshipp.env` file at the volume level? EBS volume is encrypted by default; sufficient for v0 disclosure protection.
- Application-level encryption of fields at rest (Aadhaar, etc.) is a separate concern (also deferred for v0, also a v1 readiness gate).
