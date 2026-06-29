# AI Grid Reporting API — Design Rationale & PoC Scope

**Status:** Draft
**Date:** June 26, 2026
**Related:** [cost-reports-feasibility.md](cost-reports-feasibility.md) (earlier feasibility survey — supersedes the "Koku Components to Reuse" section of that doc)

---

## Decision

The AI Grid PoC exposes a **new, independent API** for cost reporting, quota status, and metering data. The existing Koku report API endpoints (`/api/cost-management/v1/reports/...`) are not used as the customer-facing contract for this product.

---

## What We Do Borrow (Conventions, Not Shape)

These lower-level conventions from the Koku/RHCM codebase are worth preserving for consistency:

| Convention | Detail |
|---|---|
| **Auth** | Bearer token, org-scoped identity — same as RHCM |
| **Pagination** | `limit`, `offset` query params; `meta.count` in response |
| **Timestamp format** | ISO 8601 with timezone (`2026-06-26T00:00:00Z`) |
| **List envelope** | `{ "data": [...], "meta": { "count": N } }` |
| **Error shape** | `{ "errors": [{ "detail": "...", "status": 4xx }] }` |

These are generic REST conventions, not Koku-specific. They happen to align because Koku uses them too.

---

## PoC Report Requirements

The following endpoints are in scope for the PoC.

### Cost & Spend

#### `GET /costs/summary`

Aggregated spend for a billing period, broken down by resource type.

**Required query params:** `tenant_id`
**Optional:** `project_id`, `resource_type` (`caas|vmaas|bmaas|maas`), `period_start`, `period_end`
**Key response fields:** `capacity_cost`, `maas_cost`, `total_cost`, `currency`, `data_as_of`
**Target latency:** < 2 s
**Satisfies:** REQ-2, REQ-3

---

#### `GET /costs/breakdown`

Line-item cost detail with drill-down by resource, project, user, and tier applied.

**Required:** `tenant_id`
**Optional:** `project_id`, `resource_id`, `resource_type`, `user_id`, `period_start`, `period_end`, `limit`, `offset`
**Key response fields:** `resource_id`, `resource_type`, `sku`, `quantity`, `unit`, `unit_price`, `tier_applied`, `cost`
**Target latency:** < 5 s
**Satisfies:** REQ-3, REQ-3a, REQ-11 (tier_applied field)

---

#### `GET /costs/timeseries`

Cost bucketed over time at hourly or daily granularity. Supports trend views in a tenant portal.

**Required:** `tenant_id`
**Optional:** `project_id`, `resource_type`, `granularity` (`hour|day`), `period_start`, `period_end`
**Key response fields:** `bucket_start`, `bucket_end`, `capacity_cost`, `maas_cost`, `total_cost`
**Target latency:** < 5 s
**Satisfies:** REQ-2, REQ-3

---

### Metering & Usage Detail

#### `GET /metering/usage`

Raw metered quantities before cost math is applied. Source-of-truth feed for dispute resolution and custom analytics.

**Required:** `tenant_id`
**Optional:** `project_id`, `resource_id`, `resource_type`, `period_start`, `period_end`, `limit`, `offset`

**Capacity resource fields** (CaaS, VMaaS, BMaaS):
`compute_hours`, `vcpu_hours`, `ram_gib_hours`

**MaaS (consumption) fields** (OpenShift AI):
`prompt_tokens`, `completion_tokens`, `cached_tokens`, `inference_requests`, `gpu_vram_gib_seconds`

**Target latency:** < 5 s
**Satisfies:** REQ-2a, REQ-4, REQ-8

---

### Chargeback Reports

#### `GET /reports/chargeback`

Pre-assembled chargeback report merging capacity and MaaS costs into a single view per tenant/project. Exportable as JSON (default) or CSV.

**Required:** `tenant_id`, `period_start`, `period_end`
**Optional:** `project_id`, `format` (`json|csv`)
**Key response fields:** `tenant_id`, `project_id`, `compute_hours`, `gpu_hours`, `total_tokens`, `total_requests`, `capacity_cost`, `maas_cost`, `total_cost`, `currency`
**Target latency:** < 10 s (report generation)
**Satisfies:** REQ-5

---

### Quota & Budget Status

#### `GET /quotas/status`

Current quota and budget consumption for a tenant or project. OSAC calls this synchronously at resource creation time to gate provisioning.

> **Hard latency SLA: < 500 ms.** This endpoint must be backed by a materialized view or cache. It cannot query raw metering tables at request time.

**Required:** `tenant_id`
**Optional:** `project_id`, `dimension` (`tokens|compute_hours|budget`)
**Key response fields:** `quota_limit`, `quota_consumed`, `quota_pct`, `budget_limit`, `budget_consumed`, `budget_pct`, `is_exceeded`, `thresholds_breached[]`
**Satisfies:** REQ-9

---

#### `GET /quotas/threshold-events`

Audit log of threshold evaluations (50%, 70%, 90%, 100%) — when they fired, what the consumption was at trigger time, and whether the back-channel notification to OSAC was delivered.

**Required:** `tenant_id`
**Optional:** `project_id`, `threshold_pct`, `period_start`, `period_end`
**Key response fields:** `threshold_pct`, `triggered_at`, `dimension`, `consumed_at_trigger`, `limit_at_trigger`, `notification_status` (`delivered|failed|pending`)
**Target latency:** < 2 s
**Satisfies:** REQ-10, REQ-7

---

### Rates & Custom Metrics

#### `GET /rates`

Published rate card with tier breakpoints for a given resource type. Allows tenants to validate what rate was applied to a bill. Manual configuration is acceptable for PoC; API-driven sync from the OSAC service catalog is post-PoC.

**Optional:** `resource_type` (`caas|vmaas|bmaas|maas`), `effective_at`
**Key response fields:** `resource_type`, `tiers[]` — each with `tier_start`, `tier_end`, `unit`, `unit_price`, `currency`, `effective_from`
**Target latency:** < 1 s
**Satisfies:** REQ-3b, REQ-11

---

#### `GET /costs/custom-metrics`

Cost records attributed to arbitrary CloudEvent dimensions configured via the custom metrics feature. Enables new billable dimensions without code changes once the ingestion side is wired.

> **Dependency:** This endpoint is only valuable after REQ-13 ingestion-side schema config is implemented. Track together.

**Required:** `tenant_id`, `dimension_id`
**Optional:** `project_id`, `period_start`, `period_end`
**Key response fields:** `dimension_id`, `dimension_name`, `unit`, `quantity`, `unit_price`, `cost`
**Target latency:** < 5 s
**Satisfies:** REQ-13

---

## Cross-Cutting Requirements

**`data_as_of` on every response**
Every cost and metering endpoint returns a `data_as_of` (ISO 8601) field in the response body and an `X-Data-As-Of` response header. Callers can determine data freshness relative to the 60 s processing SLA.

**Tenant-scoped auth on every endpoint**
Results are always scoped to the authenticated tenant. Provider-level (cross-tenant) access requires explicit RBAC. Design for least-privilege. Open question: does OSAC need a provider-scoped token to call `/quotas/status` on behalf of any tenant? (See REQ-3a open questions.)

---

## Requirements Coverage Summary

| Requirement | Priority | Covered By |
|---|---|---|
| REQ-2 — Near-real-time cost (< 60 s) | CRITICAL | `/costs/summary`, `/costs/timeseries` |
| REQ-2a — MaaS CloudEvents | HIGH | `/metering/usage`, `/costs/summary` |
| REQ-3 — Granular cost tracking | HIGH | `/costs/breakdown`, `/costs/summary`, `/costs/timeseries` |
| REQ-3a — OSAC Tenant/Project attribution | HIGH | `tenant_id` / `project_id` params on all endpoints |
| REQ-3b — Service catalog rates | MEDIUM | `/rates` |
| REQ-4 — Token metering (MaaS) | HIGH | `/metering/usage` |
| REQ-5 — Chargeback reporting | MEDIUM | `/reports/chargeback` |
| REQ-7 — Audit & dispute tracing | STANDARD | `/quotas/threshold-events` |
| REQ-8 — Bare metal costing | HIGH | `/costs/breakdown`, `/metering/usage` (`resource_type=bmaas`) |
| REQ-9 — Quota/budget status API | HIGH | `/quotas/status` |
| REQ-10 — Threshold notification audit | HIGH | `/quotas/threshold-events` |
| REQ-11 — Cost tiers | MUST HAVE | `/rates` (tier breakpoints), `/costs/breakdown` (`tier_applied`) |
| REQ-13 — Custom rate dimensions | HIGH | `/costs/custom-metrics` |
