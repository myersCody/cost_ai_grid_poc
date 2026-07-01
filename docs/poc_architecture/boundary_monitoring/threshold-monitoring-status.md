# Threshold Monitoring — PoC Status & OSAC Questions

> **Audience:** Product Manager + OSAC team
> **Last updated:** 2026-07-01
> **Technical detail:** [alerting-spec-draft.md](alerting-spec-draft.md) | [alerting-osac-integration.md](alerting-osac-integration.md)

---

## What we're building

The goal is for Cost Management to **watch how much of their quota a tenant is consuming** and alert OSAC when usage crosses predefined thresholds — so OSAC can warn the tenant, throttle access, or block new provisioning before limits are breached.

Two capabilities are in scope:

| Capability | Requirement | How it works |
|---|---|---|
| **Quota status (pull)** | REQ-9 | OSAC asks Cost "how much has tenant X used?" at any time — e.g. before allowing a new cluster to be created |
| **Threshold alerts (push)** | REQ-10 | Cost proactively notifies OSAC the moment a tenant crosses 50 / 70 / 90 / 100% of their quota |

**Who owns what:**

| Concern | Owner |
|---|---|
| Defining quota limits and budgets | **OSAC** |
| Measuring consumption (metering) | **Cost Management** |
| Evaluating thresholds + sending alerts | **Cost Management** |
| Enforcing limits (block/throttle/warn tenant) | **OSAC** |
| Tenant-facing notifications (email, banners) | **OSAC** |

---

## Where we are today

### What's working right now

- **Quota table seeded locally** — limits for demo tenants (`tenant-acme`, `tenant-globex`, etc.) are pre-loaded at startup. Meters covered: VM CPU, VM memory, VM uptime, MaaS tokens (in/out), and MaaS request count.
- **Threshold evaluator running** — after every 60-second metering sweep, Cost checks each tenant's consumption against their quota. If a threshold is crossed, an alert record is written to the database.
- **Thresholds evaluated:** 50%, 70%, 90%, 100%
- **Pull API live** — `GET /api/v1/quotas/{tenant_id}` returns current consumption, quota limits, and which thresholds have fired. OSAC can call this today.
- **No duplicate alerts** — the database prevents the same threshold from firing more than once per period.

### What's not built yet

| Capability | Status | What's needed to build it |
|---|---|---|
| Alert resolved / cleared state | Not started | Currently alerts fire once and are never cleared, even if consumption drops |
| Push webhook to OSAC | Not started | Requires an OSAC webhook endpoint + agreement on auth method |
| Live quota sync from OSAC | Not started | Requires OSAC to expose a Quota List API; currently limits are hardcoded |
| Budget monitoring (dollar limits) | Not started | Quota (unit-based) monitoring works; cost-amount monitoring does not |
| Configurable thresholds per quota | Not started | Thresholds are hardcoded at 50/70/90/100% globally |

### PoC phase summary

| Phase | Deliverable | Status |
|---|---|---|
| P0 | Quota limits loaded | **Done** *(local seed; not yet synced from OSAC)* |
| P1 | Configurable thresholds per quota | **Skipped** *(hardcoded at 50/70/90/100%)* |
| P2 | Threshold evaluator runs after each sweep | **Done** |
| P3 | Alert lifecycle (fire → resolve → acknowledge) | **Partial** *(fire-once only; no resolution)* |
| P4 | Pull API for quota status | **Done** *(shape differs slightly from final spec)* |
| P5 | Push webhook emitter to OSAC | **Not started** |
| P6 | Budget (dollar-based) evaluation | **Not started** |

**Minimum demo ready:** A tenant's consumption can be metered, thresholds evaluated, and quota status retrieved via the pull API — end-to-end on CPU, memory, and MaaS meters.

---

## Recommended integration path

We recommend **Option 1: Push + Pull together** for v1:

- **Push** (~90 second latency after usage crosses a threshold) — Cost sends a CloudEvent to OSAC whenever a threshold fires or resolves. OSAC uses this to display console banners and notify tenant admins.
- **Pull** (sub-second) — OSAC calls Cost's quota status API at create time to enforce hard gates (OPA denies the request if the tenant is over quota).

For the **PoC demo**, we are on **Option 4**: local mock limits, pull API working, push not yet wired. This unblocks the demo without requiring OSAC to build anything yet.

---

## Questions for OSAC — Threshold Notifications (REQ-10)

These three questions are blocking the design of the push notification path. We need OSAC's input to proceed.

---

### Question 10 — Does OSAC have an alerting/webhook endpoint?

**Context:** Cost has implemented pull-based threshold checks — the quota API already returns which thresholds have been crossed. For push notifications (where Cost proactively tells OSAC something happened), we need a place to send those alerts.

**Question:** Does OSAC already have an alert ingestion endpoint that Cost can POST to? Or does OSAC need to build one?

> If OSAC needs to build it, Cost can provide the exact payload schema — see [alerting-spec-draft.md § Outbound CloudEvent](alerting-spec-draft.md#outbound-cloudevent--costquotathresholdv1).

---

### Question 11 — What alert transport does OSAC prefer?

**Context:** We need to agree on how Cost sends threshold events to OSAC. Options we've considered:

| Option | Description |
|---|---|
| **CloudEvent POST** | Industry standard envelope; Cost already uses CloudEvents for internal events |
| **Webhook with shared secret** | Simple HMAC signature verification |
| **mTLS** | Mutual TLS for strong service identity, higher operational overhead |

**Question:** What transport and authentication method does the OSAC team prefer?

---

### Question 12 — How should Cost handle the 100% threshold?

**Context:** When a tenant hits 100% of their quota, Cost currently fires a single alert. But if OSAC enforces a grace window before cutting off access, Cost needs to know — because the alert sequence would change:

- **No grace period:** One alert at 100% (`exceeded`) → access blocked.
- **Grace window:** Three alerts — `100% crossed` → `grace period started` → `grace period expired` → access blocked.

This also determines whether Cost needs to track and publish a `resolved` event (consumption dropped back below 100% before grace expired).

**Question:** Does hitting 100% quota mean immediate cutoff, or is there a grace window? If there's a grace window, how long, and is it configurable per tenant or per quota?

---

## Next steps (pending answers)

| After we know... | We can build... |
|---|---|
| OSAC webhook endpoint exists / is planned | Push emitter (P5) |
| Auth method confirmed | CloudEvent delivery with retry logic |
| Grace period semantics | Alert state machine (fire → grace → expired → resolve) |
| OSAC Quota List API available | Replace local seeds with live OSAC sync |
