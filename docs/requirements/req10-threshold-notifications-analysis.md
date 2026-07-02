# Requirement 10: Threshold Notifications — Analysis

> **Requirement:** Send threshold notifications from RHCM to OSAC when
> cost/quota consumption hits defined levels (50%, 70%, 90%, 100%).
> OSAC consumes these to trigger OPA-enforced rate limiting.
>
> **Source:** [poc_requirements_overview.md#req-10](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-10-threshold-notification-back-channel-to-osac)
>
> **Depends on:** REQ-9 (Quota status API) — **Done**
>
> **Cody's design:** [boundary_monitoring/](https://github.com/myersCody/cost_ai_grid_poc/tree/main/docs/poc_architecture/boundary_monitoring)

## What We Have

The quota status API (`GET /api/v1/quotas/{tenant_id}`) already computes
consumption vs limits and evaluates threshold flags:

```json
{
  "meter_name": "vm_cpu_core_seconds",
  "limit": 360000,
  "consumed": 252000,
  "percentage": 70.0,
  "thresholds": {"50": true, "70": true, "90": false, "100": false}
}
```

This is the **pull model** — OSAC calls us to check. REQ-10 asks for the
**push model** — we proactively notify OSAC when a threshold is crossed.

## Delivery Models

### Pull Only (what we have)

```
OSAC → GET /api/v1/quotas/tenant-acme → response includes thresholds
```

OSAC checks before creating resources. No proactive notification.
Sub-second latency. Already implemented.

### Push via Webhook (what REQ-10 asks for)

```
Rating sweep → threshold crossed → POST webhook to OSAC
```

We proactively notify OSAC. OSAC doesn't need to poll.

### Push + Pull Together (recommended by Cody's design)

- **Push:** async webhook when a threshold is crossed (notification)
- **Pull:** sync API call at resource creation time (gate check)

Both serve different purposes: push for awareness, pull for enforcement.

## What We Need to Build

### 1. Alerts Table

Track which thresholds have fired to avoid re-firing every sweep:

```sql
CREATE TABLE alerts (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    meter_name     TEXT NOT NULL,
    threshold_pct  NUMERIC NOT NULL,
    consumed       NUMERIC NOT NULL,
    limit_value    NUMERIC NOT NULL,
    period         TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'firing',
    fired_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at   TIMESTAMPTZ,
    UNIQUE(tenant_id, meter_name, threshold_pct, period)
);
```

The UNIQUE constraint ensures "70% crossed for tenant-acme's
vm_cpu_core_seconds in 2026-06" fires exactly once.

### 2. Threshold Evaluation

In the rating sweep (every 30s), after processing metering entries:

1. For each tenant with active quotas
2. Compute current consumption percentage
3. Check each threshold level (50, 70, 90, 100)
4. If crossed and not already in `alerts` → insert alert + deliver

### 3. Webhook Delivery

POST a CloudEvents-formatted notification to a configured URL:

```json
{
  "specversion": "1.0",
  "type": "cost.quota.threshold",         // proposed — to be agreed with OSAC
  "source": "cost-management",
  "id": "tenant-acme:vm_cpu_core_seconds:70:2026-06",
  "time": "2026-06-27T10:00:00Z",
  "subject": "tenant-acme",
  "data": {
    "tenant_id": "tenant-acme",
    "meter_name": "vm_cpu_core_seconds",
    "threshold": 70,
    "consumed": 252000,
    "limit": 360000,
    "period": "2026-06"
  }
}
```

Using CloudEvents format keeps consistency with the rest of the OSAC
event ecosystem.

### 4. Alert Delivery Reliability

| Strategy | Complexity | Notes |
|---|---|---|
| Fire and forget | Trivial | POST once, log if fails |
| Retry with backoff | Small | 3 retries, exponential backoff |
| Outbox pattern | Medium | Write to DB, drain asynchronously |

PoC: retry with backoff. Production: outbox pattern for guaranteed delivery.

## Open Questions

### 1. Webhook target URL

How does OSAC tell us where to send notifications?

| Option | For PoC | For Production |
|---|---|---|
| Environment variable (`ALERT_WEBHOOK_URL`) | Simplest | Too rigid |
| Registration API (`POST /api/v1/webhooks`) | Overkill | Correct |
| Config file | Acceptable | Acceptable |

### 2. Authentication

How does OSAC verify the notification came from us?

| Option | Complexity |
|---|---|
| Shared secret in header (`X-Webhook-Secret`) | Trivial |
| HMAC signature on body | Small |
| Bearer token (OSAC gives us one) | Small |
| mTLS | Medium |

### 3. Does OSAC have an alerting endpoint?

The spec says "transport TBD." OSAC architect was to consult with the
working group about existing alerting capabilities. If OSAC already has
an alert ingestion endpoint, we send to that. If not, they need to build
one.

### 4. Grace periods

The spec mentions "grace periods for budget overages may be required."
Does hitting 100% mean immediate cutoff or is there a grace window?
This affects whether we send a single "100% reached" alert or a
sequence ("100% reached" → "grace period started" → "grace expired").

### 5. Pull-only acceptable for PoC?

Cody's design suggests pull + push together, with pull-only as
"acceptable PoC interim." If the OSAC team agrees, we can defer the
webhook implementation and just enhance the quota API response to
include alert history:

```json
{
  "quotas": [...],
  "alerts": [
    {"meter_name": "vm_cpu_core_seconds", "threshold": 70, "fired_at": "2026-06-27T10:00:00Z"}
  ]
}
```

This is trivially small to implement.

## Implementation Options for PoC

### Option A: Pull-only (smallest)

Enhance the quota API to include fired alerts. No webhook. OSAC checks
before resource creation and sees which thresholds were crossed.

**Effort:** Small — add alerts table, threshold check in rating sweep,
include alerts in quota API response.

### Option B: Push + Pull (full REQ-10)

Everything in Option A plus webhook delivery to an OSAC endpoint.

**Effort:** Medium — needs webhook URL configuration, HTTP POST with
retry, CloudEvents formatting, delivery tracking.

**Blocked on:** OSAC providing an endpoint to receive notifications,
and agreeing on auth mechanism.

## Recommended Approach

Start with **Option A** (pull-only) — it's unblocked and delivers the
core value: OSAC knows which thresholds were crossed, when, and for
which meters. Add **Option B** (webhook push) when OSAC confirms their
alerting endpoint and auth mechanism.
