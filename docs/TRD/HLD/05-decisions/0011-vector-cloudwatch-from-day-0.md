# ADR 0011 — Vector → CloudWatch log shipping from day 0

Date: 2026-04-30
Status: Accepted
Owner: Architect A (after Architect C push-back)
Supersedes: prior conversational decision "stdout-only at v0; ship later"

## Context

Original plan: at v0, log to stdout, defer log shipping until later.

Architect C pointed out: on EC2 with systemd-journald, journald rotates aggressively (default ~100MB ring buffer). Three days of busy logs and you can't read day one. The first time something interesting happens on a Friday for which you didn't get a notification, by Monday the logs are gone.

Cost of fixing now: ~30 minutes. Cost of not fixing: at least one wasted incident investigation.

## Decision

**Run Vector on the EC2 host from day 0. Ship structured logs to CloudWatch Logs.**

Components:
- `pikshipp` writes JSON logs to stdout via `log/slog`.
- `pikshipp.service` (systemd) captures stdout to journald.
- `vector.service` (systemd) reads from journald, ships to CloudWatch Logs.
- CloudWatch Log Group: `/pikshipp/<env>`, 30-day retention.

## Alternatives considered

### Stdout only, no shipping (original)
- Rejected: journald rotation loses logs.

### Direct CloudWatch via AWS SDK (skip Vector)
- Possible but tightly couples app code to AWS.
- Vector decouples; lets us swap to Loki, Splunk, ES, etc. without code change.

### Fluent Bit instead of Vector
- Comparable. Either works. Vector picked for its better Rust+Go integration story and slightly simpler config.

### Self-hosted ELK
- Overkill for v0.

## Consequences

### What this commits us to
- Vector running on every EC2 instance.
- IAM role for the EC2 instance includes `logs:PutLogEvents`.
- CloudWatch Logs cost: ~$0.50/GB ingested; $0.03/GB stored. At v0 traffic, <$10/month.

### What it costs
- One more daemon to operate (Vector). Mostly self-managing; rarely needs attention.

### What it enables
- Persistent log history beyond journald retention.
- Querying via CloudWatch Logs Insights.
- Alerting via CloudWatch Log Metric Filters → CloudWatch Alarms.
- Foundation for Sentry integration in v1 (errors-from-logs trigger Sentry events).

## Vector configuration (sketch)

```toml
[sources.journald]
type = "journald"
include_units = ["pikshipp.service"]

[transforms.parse]
type = "remap"
inputs = ["journald"]
source = '''
. = parse_json!(.message)
.host = get_hostname!()
'''

[sinks.cloudwatch]
type = "aws_cloudwatch_logs"
inputs = ["parse"]
region = "ap-south-1"
group_name = "/pikshipp/${ENV}"
stream_name = "{{ host }}"
encoding.codec = "json"
```

## Operational notes

- Vector runs as its own systemd service; restart-on-failure.
- Vector buffer is on disk; survives Vector restart with no data loss.
- CloudWatch Log Group retention configured via Terraform.

## Open questions

- For v1, do we add Sentry that consumes from CloudWatch Logs (or vice versa, app code calls Sentry SDK directly)? Direct SDK is cleaner; Sentry's value is in error grouping, not log aggregation. Decide at v1 spec time.
- Cost monitoring: at scale, CloudWatch Logs ingest costs add up. Track via CloudWatch billing alerts.
