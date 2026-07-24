# Adversarial Due Diligence Review — inventory-watcher

**Version:** 1.0 | **Date:** 2026-06-28 | **Reviewer:** AI-assisted

---

## Executive Summary

The inventory-watcher is a PoC-quality Go service that implements a complete
metering-to-cost pipeline. The architecture is sound — clean separation of
concerns across watcher, reconciler, metering, rating, and ingest components.
The pipeline design (events → raw_events → inventory → metering → cost → quotas)
is well-thought-out and the OpenMeter-compatible ingest endpoint is a
strategically smart integration point.

However, the codebase has significant gaps in error handling, input validation,
authentication, and resource management that are acceptable for a PoC demo but
would be blockers for production deployment. The most impactful issues are:
silent error swallowing throughout the ingest pipeline, no authentication on
API endpoints, missing pagination in OSAC List calls, and no request limits
on the HTTP server.

**Overall assessment:** Strong architecture, PoC-appropriate implementation.
Needs hardening before production.

---

## Scorecard

| Dimension | Rating | Key gap |
|-----------|--------|---------|
| Security | ★★☆☆☆ | No auth on API endpoints; hardcoded default credentials |
| Correctness | ★★★☆☆ | Silent error swallowing in ingest pipeline; missing pagination |
| Auditability | ★★★☆☆ | raw_events provides audit trail; no request IDs or tracing |
| Operational robustness | ★★☆☆☆ | No request limits, no graceful shutdown timeouts, no health check depth |
| Performance | ★★★★☆ | 1,700 events/s throughput; N+1 query in summarizer; unbounded slices |
| Design quality | ★★★★☆ | Clean separation; shared store without transaction boundaries |
| Maintainability | ★★★☆☆ | Good structure; duplicate constants; tests cover API but not internals |
| Governance | ★★★★☆ | ADRs documented; CLAUDE.md rules; no CI pipeline |

---

## Findings Status Summary

| # | Title | Severity | Dimension | Status |
|---|-------|----------|-----------|--------|
| 1 | No authentication on API endpoints | Critical | Security | Open |
| 2 | Silent error swallowing in ingest handlers | Critical | Correctness | Open |
| 3 | Missing pagination in OSAC List calls | Critical | Correctness | **Fixed** |
| 4 | Hardcoded default database credentials | High | Security | Accepted (PoC) |
| 5 | No request size limits or timeouts on HTTP server | High | Operational | Open |
| 6 | Division by zero in rating threshold evaluation | High | Correctness | Open |
| 7 | Missing input validation on tenant/resource IDs | High | Security | Open |
| 8 | Reconciler fails silently on API errors | Medium | Correctness | Accepted (PoC) |
| 9 | No transaction boundaries in multi-insert operations | Medium | Correctness | Open |
| 10 | JSON injection in error responses | Medium | Security | Open |
| 11 | Scanner max token size limit (64KB) | Medium | Operational | Open |
| 12 | N+1 query in summarizer instance type lookup | Medium | Performance | Open |
| 13 | Duplicate event type constants across packages | Low | Maintainability | Open |
| 14 | Unbounded slice allocation in store queries | Low | Performance | Open |
| 15 | No distributed tracing or request IDs | Low | Auditability | Open |
| 16 | Implicit UTC timezone assumption | Informational | Correctness | Accepted |
| 17 | No CI pipeline or automated test enforcement | Informational | Governance | Open |

---

## Findings Detail

### #1 — No authentication on API endpoints
**Severity:** Critical | **Dimension:** Security | **Effort:** M

**Location:** `internal/api/handler.go` — `ServeMux()` (line 51)

**Description:** All HTTP endpoints (`POST /api/v1/events`, `GET /api/v1/quotas/{tenant_id}`,
`GET /api/v1/health`) are unauthenticated. Any network-accessible client can
ingest events (causing arbitrary metering/billing), query any tenant's quota
status, or enumerate tenant IDs.

**Risk:** An attacker on the network can inject fake metering events that generate
real cost entries, or query quota data for all tenants.

**Recommendation:** Add authentication middleware — at minimum a shared secret
(`X-API-Key` header) for the PoC, bearer token validation for production.
The quota endpoint should additionally verify the caller is authorized to view
the requested tenant's data.

---

### #2 — Silent error swallowing in ingest handlers
**Severity:** Critical | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/api/handler.go` — lines 153, 161, 204, 271, 280

**Description:** Store operations in event handlers discard errors with `_ =`.
If a database write fails (e.g., `UpsertComputeInstance`, `UpdateLastMetered`),
the handler returns HTTP 202 Accepted. The client believes the event was
processed, but inventory or metering data is missing.

**Risk:** Silent data loss — events accepted but not fully processed. Billing
gaps that are invisible to both the sender and the operator.

**Recommendation:** Return errors from handler functions. If a store operation
fails, return HTTP 500 (not 202). Log the error with the event ID for
correlation.

---

### #3 — Missing pagination in OSAC List calls
**Severity:** Critical | **Dimension:** Correctness | **Effort:** M

**Location:** `internal/osac/client.go` — `listAll()` (line 151)

**Description:** The `listAll` generic function fetches only the first page of
results from OSAC REST endpoints. The response contains `Page` and `Total`
fields but they are not used to paginate. If OSAC has more resources than fit
on one page, the reconciler sees an incomplete inventory.

**Risk:** The reconciler marks resources beyond page 1 as deleted (phantom
deletes), removing them from inventory and stopping their metering. This is
a data loss bug that scales with deployment size.

**Recommendation:** Implement pagination loop using the `Page`/`Total` or
cursor fields from the OSAC API response. Continue fetching until all pages
are consumed.

---

### #4 — Hardcoded default database credentials
**Severity:** High | **Dimension:** Security | **Effort:** S

**Location:** `internal/config/config.go` — line 25

**Description:** Default `INVENTORY_DB_URL` contains `postgres://user:pass@localhost:5434/costdb`.
These credentials are in source code and could be used if environment variables
are not set.

**Risk:** In a deployment where environment variables are misconfigured, the
service connects with known default credentials. Low risk for PoC, high risk
if the code is deployed without review.

**Recommendation:** Remove default credentials. Require `INVENTORY_DB_URL` to
be set explicitly (fail on startup if missing).

**Status:** Accepted for PoC — default credentials match the development
Docker containers.

---

### #5 — No request size limits or timeouts on HTTP server
**Severity:** High | **Dimension:** Operational | **Effort:** S

**Location:** `cmd/consumer/main.go` — lines 99-105

**Description:** The HTTP server has no `ReadTimeout`, `WriteTimeout`,
`MaxHeaderBytes`, or request body size limits. An attacker can send
arbitrarily large payloads or hold connections open indefinitely.

**Risk:** Denial of service via memory exhaustion (large payloads) or
connection exhaustion (slowloris).

**Recommendation:** Add server timeouts and wrap the request body with
`http.MaxBytesReader`:
```go
srv := &http.Server{
    Addr:           cfg.IngestListenAddr,
    Handler:        h.ServeMux(),
    ReadTimeout:    10 * time.Second,
    WriteTimeout:   10 * time.Second,
    MaxHeaderBytes: 1 << 20,  // 1MB
}
```

---

### #6 — Division by zero in rating threshold evaluation
**Severity:** High | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/rating/rating.go` — line 112

**Description:** `pct := (consumed / q.LimitValue) * 100` has no guard against
`q.LimitValue == 0`. The quota API handler (handler.go line 363) correctly
checks `if q.LimitValue > 0`, but the rating sweep does not.

**Risk:** A quota with `limit_value = 0` causes a division by zero, producing
Infinity or NaN, which propagates to alert comparisons.

**Recommendation:** Add `if q.LimitValue <= 0 { continue }` before the
percentage calculation in `evaluateThresholds`.

---

### #7 — Missing input validation on tenant/resource IDs
**Severity:** High | **Dimension:** Security | **Effort:** S

**Location:** `internal/api/handler.go` — `classifyEvent()` (line 295)

**Description:** Tenant IDs, resource IDs, and model IDs from incoming
CloudEvents are used directly in database operations without validation.
Empty strings, very long strings, or strings with special characters are
accepted.

**Risk:** Empty tenant IDs cause incorrect cost aggregation. Very long strings
could impact database performance. Special characters in JSON error responses
could cause injection.

**Recommendation:** Validate that required fields are non-empty and conform
to expected patterns (e.g., UUID format, max length). Return 400 on
validation failure.

---

### #8 — Reconciler fails silently on API errors
**Severity:** Medium | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/reconciler/reconciler.go` — lines 60-65

**Description:** Each reconciliation function logs API errors and returns
without propagating the error. The caller logs "reconciliation complete"
even if every API call failed.

**Risk:** Systemic OSAC API outages are masked. The operator sees
"reconciliation complete" and doesn't know the inventory is stale.

**Recommendation:** Track success/failure counts and log a warning if any
sub-reconciliation failed. For production, expose a metric.

**Status:** Accepted for PoC — the reconciler is a safety net, not primary.

---

### #9 — No transaction boundaries in multi-insert operations
**Severity:** Medium | **Dimension:** Correctness | **Effort:** M

**Location:** `internal/api/handler.go` — `handleComputeInstanceEvent` (lines 165-210)

**Description:** A single event produces multiple database writes (raw_event +
inventory upsert + 3 metering entries + last_metered_at update). These are
individual operations, not wrapped in a transaction. If the process crashes
mid-way, the database has partial data.

**Risk:** Partial metering — e.g., `vm_uptime_seconds` is inserted but
`vm_cpu_core_seconds` is not. Cost reports show inconsistent data.

**Recommendation:** Wrap multi-insert operations in a database transaction.
pgxpool supports `pool.Begin()` for this.

---

### #10 — JSON injection in error responses
**Severity:** Medium | **Dimension:** Security | **Effort:** S

**Location:** `internal/api/handler.go` — lines 98, 118

**Description:** Error messages are interpolated into JSON strings without
escaping: `fmt.Sprintf('{"error":"%s"}', err)`. If the error message
contains quotes or backslashes, the JSON is malformed or injectable.

**Recommendation:** Use `json.Marshal` to construct error responses, or
use a helper function that properly escapes the error string.

---

### #11 — Scanner max token size limit (64KB)
**Severity:** Medium | **Dimension:** Operational | **Effort:** S

**Location:** `internal/osac/client.go` — line 94

**Description:** `bufio.Scanner` defaults to 64KB max line size. If OSAC
sends an event larger than 64KB (e.g., a resource with many labels or
large annotations), scanning stops silently.

**Recommendation:** Set a larger buffer:
```go
scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)  // 1MB
```

---

### #12 — N+1 query in summarizer instance type lookup
**Severity:** Medium | **Dimension:** Performance | **Effort:** S

**Location:** `internal/summarizer/summarizer.go` — lines 75-79

**Description:** For each compute instance missing core/memory data, a
separate `GetInstanceType` query is executed. With 10,000 instances,
this is 10,000 extra queries.

**Recommendation:** Batch-fetch all instance types into a map before the
loop.

---

### #13 — Duplicate event type constants
**Severity:** Low | **Dimension:** Maintainability | **Effort:** S

**Location:** `internal/api/handler.go` lines 68-72, `internal/osac/types.go` lines 131-135

**Description:** Event type string constants are defined in two separate
packages with different names. No single source of truth.

**Recommendation:** Define all event type constants in `internal/osac/types.go`
and import them in `internal/api/handler.go`.

---

### #14 — Unbounded slice allocation in store queries
**Severity:** Low | **Dimension:** Performance | **Effort:** S

**Location:** `internal/inventory/store.go` — multiple query functions

**Description:** Query result slices are created with zero capacity and
grown via `append`, causing repeated re-allocations.

**Recommendation:** Pre-allocate with a reasonable capacity estimate.

---

### #15 — No distributed tracing or request IDs
**Severity:** Low | **Dimension:** Auditability | **Effort:** M

**Description:** Events flow through the pipeline without a trace ID. If
an event is ingested but metering entries don't appear, there's no way
to trace it through the system other than matching `event_id` manually.

**Recommendation:** Add a request ID to each HTTP request and propagate
it through log entries via context.

---

### #16 — Implicit UTC timezone assumption
**Severity:** Informational | **Dimension:** Correctness

**Description:** All time operations use `time.Now().UTC()` and monthly
period boundaries are calculated in UTC. This is consistent but not
documented as a requirement.

**Status:** Accepted — UTC is the correct choice for billing.

---

### #17 — No CI pipeline
**Severity:** Informational | **Dimension:** Governance

**Description:** No GitHub Actions, Makefile targets for lint/test, or
pre-commit hooks. Tests exist but are not enforced.

**Recommendation:** Add a basic CI pipeline: `go vet`, `go build`,
`go test ./...` on pull requests.

---

## Priority Remediation Order

| Priority | Finding | Effort | Impact |
|---|---|---|---|
| 1 | #6 Division by zero in rating | S | Prevents crashes |
| 2 | #2 Silent error swallowing | S | Prevents data loss |
| 3 | #10 JSON injection in errors | S | Prevents injection |
| 4 | #7 Input validation | S | Prevents bad data |
| 5 | #5 HTTP server limits | S | Prevents DoS |
| 6 | #11 Scanner buffer size | S | Prevents silent stream death |
| 7 | #3 OSAC List pagination | M | Prevents phantom deletes |
| 8 | #1 Authentication | M | Prevents unauthorized access |
| 9 | #9 Transaction boundaries | M | Prevents partial writes |
| 10 | #12 N+1 query fix | S | Performance improvement |

---

## Accepted Risks

| # | Finding | Rationale |
|---|---------|-----------|
| 4 | Default credentials | PoC only; matches dev Docker containers |
| 8 | Reconciler silent failures | Safety net, not primary data path |
| 16 | UTC assumption | Correct for billing; documented |

---

## Strengths (What's Done Well)

- **Pipeline architecture** — clean event → metering → rating → cost flow
  with `metering_entries` as the stable interface boundary
- **Raw event log** — immutable audit trail with deduplication
- **Billable state filtering** — only RUNNING/READY resources are metered
- **Final metering on DELETE** — no usage lost between sweep and deletion
- **ADR documentation** — architectural decisions are well-documented
- **OpenMeter compatibility** — strategic integration point requiring zero
  changes on OSAC side
- **Throughput** — 1,700 events/s on a laptop is more than adequate

---

## Current State

| Category | Count |
|---|---|
| Total findings | 17 |
| Fixed | 9 (#1, #2, #3, #5, #6, #7, #10, #11, #12) |
| Accepted | 3 (#4, #8, #16) |
| Open | 5 (#9, #13, #14, #15, #17) |

### Fixes Applied (v1.1, 2026-06-30)

- **#1** No auth → JWT middleware compatible with OSAC (authn only; authz gap documented)
- **#2** Error swallowing → handlers return errors; 500 on failure
- **#3** Missing pagination → offset/limit loop, 100 per page, until total reached
- **#5** HTTP limits → ReadTimeout 10s, WriteTimeout 10s, MaxBytesReader 1MB
- **#6** Division by zero → guard `q.LimitValue <= 0`
- **#7** Input validation → required fields, length caps, unmarshal error handling
- **#10** JSON injection → `writeErrorJSON` helper with `json.NewEncoder`
- **#11** Scanner buffer → 64KB → 1MB
- **#12** N+1 query → batch instance type lookup via `ListAllInstanceTypes`

---

### Fix Verification (2026-07-03, against `main` branch)

| # | Finding | Claimed | Verified | Residual |
|---|---------|---------|----------|----------|
| 1 | No auth on API endpoints | Fixed | **PARTIALLY FIXED** | Two gaps — see notes below |
| 2 | Silent error swallowing | Fixed | **FIXED** | Minor: `MeterMaaSEvent` void return |
| 3 | Missing OSAC pagination | Fixed | **FIXED** | Clean |
| 5 | No HTTP server limits | Fixed | **FIXED** | Clean |
| 6 | Division by zero in rating | Fixed | **FIXED** | Clean |
| 7 | Missing input validation | Fixed | **FIXED** | Minor: `handleBalanceCheck` URL param unchecked |
| 10 | JSON injection in errors | Fixed | **FIXED** | Clean |
| 11 | Scanner buffer size | Fixed | **FIXED** | Clean |
| 12 | N+1 query in summarizer | Fixed | **FIXED** | Minor: `ListAllInstanceTypes` error discarded |

**Result:** 8 of 9 cleanly fixed. #1 is partially fixed with two residual issues.

#### #1 — Residual A: Auth silently disabled when `AUTH_ISSUER_URL` unset

`authn/middleware.go:44-47`: when `issuerURL` is empty, `New()` logs a WARN
and `Wrap()` becomes a pass-through — all requests pass unauthenticated.
`config.go:Validate()` does not require `AUTH_ISSUER_URL`. A deployment that
forgets this env var silently runs wide open.

#### #1 — Residual B: K8s probes blocked by auth

`authn/middleware.go:94` only exempts `/api/v1/health` from JWT auth.
`handler.go:127-128` registers `/healthz` and `/readyz` — these are NOT
exempt. When auth is enabled, kubelet probes get 401, causing pod kills.

**Note:** PR #9 (observability branch) fixes residual B by changing the
exemption to `case "/healthz", "/readyz", "/metrics"`.

#### #2 — Residual: `MeterMaaSEvent` void return

`metering.go:307`: `MeterMaaSEvent` has no error return. Individual
`InsertMeteringEntry` failures inside are logged but not propagated.
`handleModelEvent` returns `nil` → client gets HTTP 202 even if metering
entries were not created. The model upsert itself does fail properly, and
the raw event is always stored.

#### #7 — Residual: `handleBalanceCheck` URL param

`handler.go:611`: extracts `customerID` from URL path without length check.
Read-only GET with parameterized queries — no injection risk, just
inconsistent with the validation on other endpoints.

#### #12 — Residual: error discarded in summarizer

`summarizer.go:53`: `allTypes, _ := s.store.ListAllInstanceTypes(ctx)`.
If the query fails, type map is empty and instances needing enrichment
silently get zero-value cores/memory.
