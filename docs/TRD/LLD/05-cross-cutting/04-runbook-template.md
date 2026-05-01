# Runbook Template

## Purpose

This is the template for incident runbooks under `docs/runbooks/`. Every alert that pages the on-call **must** have a runbook. A runbook answers four questions in order: **what alerted, is it real, what to do now, and how to confirm it's fixed**.

The template below is what ops fills in for each alert. The structure is fixed; the content is alert-specific.

---

# Runbook: <Alert Name>

> Example: `Runbook: Carrier Breaker Stuck Open (delhivery)`

## TL;DR

One paragraph. What's failing, what's the user-visible symptom, what's the typical cause, what's the typical fix. Aim for under 100 words.

> **Example:** Delhivery's circuit breaker has been in `open` state for > 10 minutes. Booking via Delhivery is blocked; allocation routes around to other carriers. Most common cause is a Delhivery API outage or our credentials being rotated upstream. First, confirm via Delhivery's status page; if their side is up, check the breaker's recent failures for auth errors and rotate credentials.

## Severity

`critical` | `warn` | `info`

What pages on-call vs. what posts to Slack. Critical means a human must look immediately.

## Owner

Team or person who owns this surface. Responsible for keeping the runbook current.

## Linked Dashboards & Logs

- Grafana: `<dashboard-link>`
- Logs query: `<saved cloudwatch query>`
- Trace query: `<honeycomb query>`

## Triggers

- Alert query / threshold (the exact PromQL or rule).
- Where the alert fires from (Grafana / CloudWatch / external).

> Example:
> ```
> max(carrier_breaker_state{carrier="delhivery"}) by (instance) == 1   // 1 = open
> for: 10m
> ```

## Symptoms

What does this look like in practice for users / sellers / ops?

> Example:
> - Sellers see "Booking failed: carrier unavailable" toasts.
> - Allocation logs show `carrier=delhivery state=open skipping`.
> - Pending shipment count climbs slowly.

## First Steps (1-5 minutes)

A linear checklist of low-cost, no-side-effect checks. Each step takes < 60 seconds.

> 1. Check Delhivery status page: <link>.
> 2. Run: `kubectl logs -l app=pikshipp,role=worker -n pikshipp --tail=200 | grep delhivery`
> 3. Query: `SELECT * FROM carrier_health_state WHERE carrier_code = 'delhivery';`
> 4. Sample failed bookings: see [logs query] for last 30 min.

## Diagnosis

Branch on what you saw above. **Use bullet points; don't write paragraphs.**

> ### If Delhivery's status page shows incident
> Their problem. Acknowledge the alert. Wait. Notify ops Slack so support has context.
>
> ### If status page green, our errors show 401 / "Client name not found"
> Credentials issue. Likely they rotated keys or our token expired.
>  - Check `secrets/carrier/delhivery` was rotated recently: `aws ssm get-parameter ...`
>  - If yes: confirm with vendor account owner.
>  - If no: contact Delhivery account manager.
>
> ### If status page green, our errors show timeouts
> Network or routing issue.
>  - Check our egress: `kubectl exec -it <pod> -- curl -v https://track.delhivery.com/healthz`
>  - If failing from our side: investigate VPC / NAT.
>  - If succeeding: check the per-call timeout — may be tuned too aggressively for current Delhivery latency.
>
> ### If errors show no consistent pattern
> Open a Sev-2 incident; engage backend on-call for deeper investigation.

## Mitigation

Concrete actions, in priority order. Each one is reversible or clearly noted otherwise.

> ### Option A: Force breaker to half-open (probe carrier)
> ```sql
> UPDATE carrier_health_state SET state='half_open', failure_count=0, half_open_slots=3 WHERE carrier_code='delhivery';
> NOTIFY carrier_health, 'delhivery';
> ```
> Effect: next 3 calls become probes. If they succeed, breaker closes; otherwise re-opens.
> Reversible: yes, automatic.
>
> ### Option B: Disable Delhivery via policy
> ```sql
> -- Block Delhivery for new bookings while preserving in-flight shipments
> SELECT policy.set_global('carrier.delhivery.enabled', 'false', '<operator>', '<reason>');
> ```
> Effect: allocation skips Delhivery; existing shipments continue tracking.
> Reversible: set back to 'true'; cache TTL ~30s.
>
> ### Option C: Trigger credential rotation
> Run rotation playbook: <link>. **Operator-only**, requires admin role.

## Verification

How do you confirm the issue is resolved?

> 1. `carrier_breaker_state{carrier="delhivery"} == 0` for 5 consecutive minutes.
> 2. Successful Delhivery bookings observable in `shipment_booked_total{carrier="delhivery"}` rate.
> 3. No new entries in `failed_shipment_count{carrier="delhivery"}` after recovery.

## Post-Incident

- Open a follow-up ticket if mitigation was Option B (re-enable Delhivery once stable).
- If credentials rotated, update the rotation log.
- If this was an unknown failure mode, append a new "Diagnosis" branch above.
- Schedule a post-mortem **only** if user-visible impact > 30 minutes OR data integrity was at risk.

## References

- Service LLD: <link to relevant LLD section>
- Related runbooks: <list>
- Past incidents: <list of post-mortem links>

---

## Runbooks We Need at v0

The following alerts must each have a runbook before launch:

| Alert | Owner | Severity |
|---|---|---|
| `CarrierBreakerStuckOpen` | platform | critical |
| `WalletInvariantCheckFailed` | platform | critical |
| `AuditChainBrokenForSeller` | platform | critical |
| `OutboxLagHigh` | platform | critical |
| `RiverQueueDepthHigh` | platform | critical |
| `PostgresReplicationLagHigh` | platform | critical |
| `PostgresConnectionsHigh` | platform | warn |
| `APIErrorRateElevated` | platform | critical |
| `APIp99LatencyHigh` | platform | warn |
| `CODSettlementLagHigh` | platform | warn |
| `NDRAutoRTOSweepStuck` | platform | warn |
| `RecoveryReconcileNotRunning` | platform | critical |
| `SESAccountSuspended` | platform | critical |
| `MSG91OutOfCredits` | platform | critical |
| `S3HealthCheckFailed` | platform | warn |
| `SLABreachSurge` | support | warn |

## Style Rules

- **No prose paragraphs** in steps. Bullets and code blocks.
- Every command must be **copy-paste runnable**. No placeholders that aren't obvious (`<seller-id>` is fine; `<thing>` is not).
- **Mark destructive actions explicitly** with ⚠️ and a one-line "what this does."
- Date the runbook at the bottom: `Last updated: 2026-05-14`. Out-of-date runbooks are worse than missing ones.

## Where Runbooks Live

```
docs/runbooks/
├── README.md                    # index
├── carrier-breaker-stuck-open.md
├── wallet-invariant-failed.md
├── outbox-lag-high.md
└── ...
```

The README has one-line summary + alert name + severity for fast triage.

## References

- LLD §02-infrastructure/03-observability: alert routing.
- LLD §05-cross-cutting/03-deployment: how to access kubectl / cloud creds.
- LLD §03-services/12-carriers-framework: breaker mechanics referenced in carrier runbooks.
- LLD §03-services/05-wallet: invariant-check job referenced in wallet runbooks.
