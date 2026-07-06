# Adversarial Due Diligence Review — Cost Event Consumer

## Version & Date

Version: 4.0 | Date: 2026-07-05 | Reviewer: AI-assisted

**Scope:** Full application review — all code since v3.0 (2026-07-03).
New code: DISABLE_COMPONENTS env var, IPP integration test, 204
response change, project dimension in metering/cost pipeline, batch
inserts, schema optimizations, stress test infrastructure.

**Base:** v3.0 review (41 findings; 22 fixed, 8 accepted, 11 open)

## Executive Summary

This review covers ~30 commits of significant engineering since v3,
including IPP gateway integration testing (Istio + ext_proc), stress
testing (850 req/s), schema optimizations (ADR-004 unique index
drop), and the project dimension addition to metering/cost.

The codebase has improved substantially in test coverage and CI
infrastructure. However, the rapid feature work has introduced
several correctness bugs and left security gaps that need attention
before any production use:

1. **Critical bug:** `InsertMeteringEntryBatch` silently drops
   `project_id`, breaking all sweep-based metering project attribution.
2. **Stored XSS** in the debug dashboard via `innerHTML` rendering of
   user-controllable data (tenant_id, resource_id).
3. **No event dedup** after ADR-004 removed the unique index — duplicate
   events produce duplicate billing.
4. **Negative duration billing** — no validation on `DurationSeconds`,
   allowing negative metering entries that reduce tenant costs.
5. **Unbounded reconciliation** — `POST /api/v1/reconcile` spawns
   unlimited goroutines with `context.Background()`.

The service is well-structured for a PoC and performs excellently
under load. The main gaps are around data integrity guards and input
validation — exactly the kind of thing that bites in production.

## Scorecard

| Dimension | v3 | v4 | Key change |
|-----------|----|----|------------|
| Security | ★★★☆☆ | ★★★☆☆ | Stored XSS, wildcard CORS, cross-tenant reads |
| Correctness | ★★★★☆ | ★★★☆☆ | Batch insert drops project_id, no event dedup, negative durations |
| Auditability | ★★★★☆ | ★★★★☆ | Good logging; sweep health not surfaced |
| Operational robustness | ★★★★☆ | ★★★☆☆ | Unbounded reconcile, no stream keepalive, readyz incomplete |
| Performance | ★★★★☆ | ★★★☆☆ | N+1 in rating sweep, LEFT JOIN anti-pattern in unrated query |
| Design quality | ★★★★★ | ★★★★☆ | Store is a God object (1300+ lines, 50 methods) |
| Maintainability | ★★★★☆ | ★★★☆☆ | No tests for watcher/reconciler/authn/store |
| Governance | ★★★★☆ | ★★★★☆ | Good CI; missing ADRs for auth model and tenant attribution |

## v3 Open Findings — Status Check

| v3 # | Title | v4 Status |
|-------|-------|-----------|
| 9 | No transaction boundaries | Open (carry-forward) |
| 13 | Duplicate event constants | Open (carry-forward) |
| 14 | Unbounded slice allocation | Open (carry-forward) |
| 17 | No CI pipeline | **Fixed** (ci.yml + integration.yml + ipp-integration.yml) |
| 23 | `normalizePath` fragility | Partially fixed (carry-forward) |
| 24 | `rand.Read` error ignored | Open (carry-forward) |
| 25 | Request ID not in context | Open (carry-forward) |
| 26 | No middleware tests | Open (carry-forward) |
| 30 | `LiveModels` gauge never set | Open (carry-forward) |
| 31 | No sweep error metrics | Open (carry-forward) |
| 34 | `toFloat64` partial string parse | Open (carry-forward) |
| 35 | Negative meter values accepted | Open (carry-forward) |
| 40 | Shadow warning unclear | Open (carry-forward) |

## Findings Status Summary — All Findings (v1–v4)

| # | Title | Severity | Version | Status |
|---|-------|----------|---------|--------|
| 1 | No auth on API endpoints | Critical | v1 | Fixed |
| 2 | Silent error swallowing | Critical | v1 | Fixed |
| 3 | Missing OSAC pagination | Critical | v1 | Fixed |
| 4 | Hardcoded default credentials | High | v1 | Accepted (PoC) |
| 5 | No HTTP server limits | High | v1 | Fixed |
| 6 | Division by zero in rating | High | v1 | Fixed |
| 7 | Missing input validation | High | v1 | Fixed |
| 8 | Reconciler silent failures | Medium | v1 | Accepted (PoC) |
| 9 | No transaction boundaries | Medium | v1 | Open |
| 10 | JSON injection in errors | Medium | v1 | Fixed |
| 11 | Scanner buffer size | Medium | v1 | Fixed |
| 12 | N+1 query in summarizer | Medium | v1 | Fixed |
| 13 | Duplicate event constants | Low | v1 | Open |
| 14 | Unbounded slice allocation | Low | v1 | Open |
| 15 | No request IDs/tracing | Low | v1 | Fixed |
| 16 | UTC timezone assumption | Info | v1 | Accepted |
| 17 | No CI pipeline | Info | v1 | **Fixed** |
| 18 | `safeGo` silently kills goroutine | High | v2 | Fixed |
| 19 | Unbounded `tenant_id` metric label | High | v2 | Fixed |
| 20 | Middleware ordering hides panics | High | v2 | Fixed |
| 21 | Metrics server hard close | Medium | v2 | Fixed |
| 22 | `/metrics` auth exemption | Medium | v2 | Fixed |
| 23 | `normalizePath` fragility | Medium | v2 | Partially fixed |
| 24 | `rand.Read` error ignored | Low | v2 | Open |
| 25 | Request ID not in context | Low | v2 | Open |
| 26 | No middleware tests | Low | v2 | Open |
| 27 | Stale resource gauges | Low | v2 | Accepted (PoC) |
| 28 | 404 path cardinality attack | Medium | v2 | Fixed |
| 29 | Probe log noise | Low | v2 | Fixed |
| 30 | `LiveModels` gauge never set | Low | v2 | Open |
| 31 | No sweep error metrics | Low | v2 | Open |
| 32 | Panic response Content-Type | Info | v2 | Fixed |
| 33 | Import grouping | Info | v2 | Fixed |
| 34 | `toFloat64` partial string parse | Medium | v3 | **Fixed** (commit a713817) |
| 35 | Negative meter values accepted | Medium | v3 | **Fixed** (commit a713817) |
| 36 | Config-driven metric cardinality | Medium | v3 | Accepted (PoC) |
| 37 | Double JSON unmarshal | Low | v3 | Accepted (PoC) |
| 38 | Classify fallback overwrites | Low | v3 | Accepted (PoC) |
| 39 | No config hot-reload | Low | v3 | Accepted (PoC) |
| 40 | Shadow warning unclear | Low | v3 | Open |
| 41 | `data.` prefix stripping | Info | v3 | Accepted |
| **42** | **`InsertMeteringEntryBatch` drops `project_id`** | **Critical** | v4 | **Open** |
| **43** | **Stored XSS in debug dashboard** | **High** | v4 | **Open** |
| **44** | **Unbounded concurrent reconciliation** | **High** | v4 | **Open** |
| **45** | **No event dedup after unique index removal** | **High** | v4 | **Open** |
| **46** | **Negative DurationSeconds not validated** | **High** | v4 | **Open** |
| **47** | **Cross-tenant data access in quota/balance endpoints** | **High** | v4 | **Deferred** (needs RBAC/authz model — [open question #18](../requirements/osac-open-questions.md)) |
| **48** | **Watch stream has no read timeout** | **High** | v4 | **Open** |
| **49** | **`/readyz` does not reflect component health** | **High** | v4 | **Open** |
| **50** | **Rating sweep N+1 queries** | **High** | v4 | **Open** |
| **51** | **`UnratedMeteringEntries` LEFT JOIN anti-pattern** | **High** | v4 | **Open** |
| **52** | **No tests for watcher/reconciler/authn/store** | **High** | v4 | **Open** |
| **53** | **CSV injection in cost report export** | **Medium** | v4 | **Open** |
| **54** | **Wildcard CORS on sensitive endpoints** | **Medium** | v4 | **Accepted (PoC)** — useful for port-forward/dev testing |
| **55** | **No rate limiting on event ingestion** | **Medium** | v4 | **Open** |
| **56** | **Debug dashboard enabled by default** | **Medium** | v4 | **Deferred** (post-PoC) |
| **57** | **Non-transactional metering + last_metered_at update** | **Medium** | v4 | **Open** |
| **58** | **Silent NodeSets JSON parse failure** | **Medium** | v4 | **Fixed** |
| **59** | **`projectCache` never invalidates** | **Medium** | v4 | **Fixed** |
| **60** | **OSAC `listAll` pagination unbounded** | **Medium** | v4 | **Open** |
| **61** | **Store is a God object (1300+ lines, 50 methods)** | **Medium** | v4 | **Accepted (PoC)** |
| **62** | **Handler mixes routing, processing, and business logic** | **Medium** | v4 | **Accepted (PoC)** |
| **63** | **Missing ADRs for auth model and tenant attribution** | **Medium** | v4 | **Open** |
| **64** | **CI lacks golangci-lint / staticcheck** | **Medium** | v4 | **Open** |
| **65** | **Watcher backoff never resets after successful connection** | **Medium** | v4 | **Fixed** |
| **66** | **Hardcoded metering and rating intervals** | **Low** | v4 | **Open** |
| **67** | **Duplicated `thresholdLevels` variable** | **Low** | v4 | **Open** |
| **68** | **Rating sweep silently skips unrated entries forever** | **Low** | v4 | **Open** |
| **69** | **Swallowed `json.Marshal` errors in watcher** | **Low** | v4 | **Open** |
| **70** | **Containerfile Go version mismatch** | **Low** | v4 | **Open** |
| **71** | **DurationMs truncation loses sub-second precision** | **Low** | v4 | **Open** |
| **72** | **Inconsistent JSON response patterns** | **Low** | v4 | **Accepted (PoC)** |

## New Findings Detail (v4)

### #42 — `InsertMeteringEntryBatch` drops `project_id` [CRITICAL]

**Dimension:** Correctness
**Location:** `internal/inventory/store.go:351-362`

`InsertMeteringEntryBatch` lists 9 columns but omits `project_id`.
The single-row `InsertMeteringEntry` (line 332) correctly includes all
10 columns. All sweep-based metering (VMs, clusters, bare metal) uses
the batch path, so every sweep-generated metering entry has
`project_id = ''`.

**Risk:** Project-level cost reporting returns zero for all
sweep-metered resources. Quota evaluation by project silently fails.

**Fix:** Add `project_id` to the batch INSERT column list and args:
```go
query := "INSERT INTO metering_entries (raw_event_id, resource_type, resource_id, tenant_id, project_id, meter_name, value, unit, period_start, period_end) VALUES "
args := make([]interface{}, 0, len(entries)*10)
// ... base := i * 10 ...
args = append(args, e.RawEventID, e.ResourceType, e.ResourceID,
    e.TenantID, e.ProjectID, e.MeterName, e.Value, e.Unit, e.PeriodStart, e.PeriodEnd)
```

**Effort:** S (minutes)

---

### #43 — Stored XSS in debug dashboard [HIGH]

**Dimension:** Security
**Location:** `internal/ingest/dashboard_embed.go:281`

The dashboard builds table rows via `innerHTML`:
```js
tbody.innerHTML = report.data.map(row => {
  return '<tr>' + '<td>' + row.group + '</td>' + ...
```

`row.group` contains tenant_id, resource_id, etc. from the database.
An attacker sends a CloudEvent with `tenant_id` set to
`<img src=x onerror=alert(document.cookie)>`, which gets stored and
rendered as HTML.

**Risk:** Script execution in admin's browser when viewing the
dashboard. Can steal session tokens or perform actions as admin.

**Fix:** Use `textContent` instead of `innerHTML` for data cells, or
escape HTML entities before interpolation.

**Effort:** S (30 minutes)

---

### #44 — Unbounded concurrent reconciliation [HIGH]

**Dimension:** Operational robustness
**Location:** `internal/ingest/handler.go:709`

`go h.reconciler.ReconcileAll(context.Background())` — no concurrency
guard, no cancellation on shutdown. Repeated POSTs to
`/api/v1/reconcile` spawn unlimited goroutines, each making full
OSAC API sweeps and bulk DB writes.

**Risk:** Resource exhaustion, OSAC API hammering, OOM.

**Fix:** (1) Add `sync.Mutex` or channel(1) to reject concurrent
reconciles. (2) Use server lifecycle context instead of
`context.Background()`. (3) Return 429 if already in progress.

**Effort:** S (1 hour)

---

### #45 — No event dedup after unique index removal [HIGH]

**Dimension:** Correctness
**Location:** `internal/inventory/store.go:312-326`, ADR-004

After ADR-004 dropped the unique index on `raw_events.event_id`,
`InsertRawEvent` always returns `(true, nil)`. The handler's
duplicate-detection branch (handler.go:197-202) is dead code.
Duplicate CloudEvents produce duplicate metering and cost entries.

**Risk:** Double-billing from replayed or retried events.

**Fix:** Add application-level dedup with a bounded in-memory LRU
cache of recently-seen event IDs, or add dedup at the metering level
with a unique constraint on `(resource_id, meter_name, period_start)`.

**Effort:** M (1-2 days)

---

### #46 — Negative DurationSeconds not validated [HIGH]

**Dimension:** Correctness
**Location:** `internal/ingest/handler.go:36,250,256,286`

`DurationSeconds` is `int` — can be negative. Used directly in:
- `periodStart = ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second)`
- metering entry value: `float64(data.DurationSeconds)`

Negative duration creates a `periodStart` in the future and negative
metering value, reducing tenant billed consumption.

**Risk:** Malicious or buggy collector sends `duration_seconds: -86400`,
creating a -1 day metering entry that reduces the tenant's bill.

**Fix:** `if data.DurationSeconds <= 0 { writeErrorJSON(w, ...); return }`

**Effort:** S (minutes)

---

### #47 — Cross-tenant data access in quota/balance endpoints [HIGH]

**Dimension:** Security
**Location:** `internal/ingest/handler.go:445-512,638-688`

Quota and balance endpoints extract tenant/customer ID from the URL
path. JWT claims are stored in context but never checked against the
requested tenant. Any authenticated user can query any tenant's
quotas and balance.

**Risk:** Tenant A queries `/api/v1/quotas/tenant-B` and sees B's
quota limits, consumption, and alert history.

**Fix:** Compare JWT `sub` or `groups` claim against the requested
tenant_id. Reject mismatches unless admin role.

**Effort:** M (1 day — needs auth model ADR first)

---

### #48 — Watch stream has no read timeout [HIGH]

**Dimension:** Operational robustness
**Location:** `internal/osac/client.go:67-70`

The streaming HTTP client has explicitly no timeout. If the server
stops sending data without closing the TCP connection (half-open),
`scanner.Scan()` blocks indefinitely — events are silently missed.

**Risk:** Network partition causes silent event loss for 15+ minutes
until the TCP stack times out.

**Fix:** Add an application-level read deadline or heartbeat timeout
(e.g., expect at least a keepalive every 60s, reconnect if not).

**Effort:** M (1 day)

---

### #49 — `/readyz` does not reflect component health [HIGH]

**Dimension:** Operational robustness
**Location:** `internal/ingest/handler.go:719-730`

`/readyz` only pings the database. If the watcher goroutine dies,
metering sweep stalls, or rating sweep crashes, `/readyz` still
returns 200. Kubernetes keeps routing traffic to a pod with a dead
pipeline.

**Risk:** Silent data loss — events accepted but never metered/rated.

**Fix:** Track last-successful-sweep timestamps per component. Fail
`/readyz` if any component exceeds 2x its expected interval.

**Effort:** M (1 day)

---

### #50 — Rating sweep N+1 queries [HIGH]

**Dimension:** Performance
**Location:** `internal/rating/rating.go:40-90`

For each unrated metering entry (up to 500), the sweep calls
`FindRate()` individually + `InsertCostEntry()` individually =
~1001 queries per sweep cycle.

**Risk:** At scale, sweep cycles exceed the 30s interval. Backlog
never drains.

**Fix:** Pre-fetch all rates into a map, batch-insert cost entries.

**Effort:** M (1 day)

---

### #51 — `UnratedMeteringEntries` LEFT JOIN anti-pattern [HIGH]

**Dimension:** Performance
**Location:** `internal/inventory/store.go:1009-1034`

`LEFT JOIN cost_entries ... WHERE ce.id IS NULL` scans the join of
two ever-growing append-only tables. Gets progressively slower.

**Risk:** After days of production, this query takes seconds+,
stalling the rating pipeline.

**Fix:** Add `rated_at TIMESTAMPTZ` column to `metering_entries` and
query `WHERE rated_at IS NULL` to avoid the join.

**Effort:** M (1 day)

---

### #52 — No tests for watcher/reconciler/authn/store [HIGH]

**Dimension:** Maintainability
**Location:** `internal/watcher/`, `internal/reconciler/`,
`internal/authn/`, `internal/inventory/`

Four packages with significant business logic have zero test files.
The watcher handles event parsing and reconnection. The reconciler
handles drift detection. The authn middleware handles JWT validation.
The store is 1300+ lines with 50 methods.

**Risk:** Regressions in event handling, auth bypass, or data access
go undetected before production.

**Fix:** Add unit tests with mock stores. Priority: authn (security),
watcher (data flow), store (data integrity).

**Effort:** L (1-2 weeks)

---

### #53 — CSV injection in cost report export [MEDIUM]

**Dimension:** Security
**Location:** `internal/ingest/handler.go:591-598`

`row.Group` written directly via `fmt.Fprintf` without escaping.
Values like `=cmd|'/C calc'!A0` in tenant_id execute when opened
in Excel.

**Fix:** Use `encoding/csv` writer, or prefix cells starting with
`=`, `+`, `-`, `@` with a single-quote.

**Effort:** S (30 minutes)

---

### #54 — Wildcard CORS on sensitive endpoints [MEDIUM]

**Dimension:** Security
**Location:** `internal/ingest/handler.go:537,613,639,691`

`Access-Control-Allow-Origin: *` on cost report, pipeline summary,
balance check, and debug config endpoints. When auth is disabled,
any website can read cost data from a user's browser.

**Fix:** Remove wildcard CORS or restrict to known origins.

**Effort:** S (minutes)

---

### #55 — No rate limiting on event ingestion [MEDIUM]

**Dimension:** Security
**Location:** `internal/ingest/handler.go:129`

No per-client or per-tenant rate limit on `POST /api/v1/events`.
Each event causes DB writes. With no event dedup (#45), floods
create unbounded billing entries.

**Fix:** Token bucket per source/tenant. Even a generous limit
(1000/min) prevents flooding.

**Effort:** S (1 hour)

---

### #56 — Debug dashboard enabled by default [MEDIUM]

**Dimension:** Security/Governance
**Location:** `internal/config/config.go:119`

`DEBUG_DASHBOARD` defaults to `true`. When auth is disabled, exposes
operational config, cost data, and pipeline state to anyone.

**Fix:** Default to `false`. Require explicit opt-in.

**Effort:** S (minutes)

---

### #57 — Non-transactional metering + last_metered_at update [MEDIUM]

**Dimension:** Correctness
**Location:** `internal/metering/metering.go:85-97`

Metering entries are inserted, then `last_metered_at` is updated in
a separate call. Crash between them duplicates metering on restart.

**Fix:** Wrap in a DB transaction.

**Effort:** S (1 hour)

---

### #58 — Silent NodeSets JSON parse failure [MEDIUM]

**Dimension:** Correctness
**Location:** `internal/metering/metering.go:202`

`_ = json.Unmarshal(cl.NodeSetsJSON, &nodeSets)` — silently skips
worker node metering on malformed JSON.

**Fix:** Log a warning on unmarshal failure.

**Effort:** S (minutes)

---

### #59 — `projectCache` never invalidates [MEDIUM]

**Dimension:** Correctness
**Location:** `internal/inventory/store.go:29-44`

`sync.Map` cache for tenant→project never expires or invalidates
on `UpsertProject`. Project reassignment requires pod restart.

**Fix:** Invalidate on `UpsertProject`, or add TTL.

**Effort:** S (30 minutes)

---

### #60 — OSAC `listAll` pagination unbounded [MEDIUM]

**Dimension:** Operational robustness
**Location:** `internal/osac/client.go:172-205`

Loops until `len(all) >= result.Total`. No upper bound on pages or
aggregate timeout. Misbehaving OSAC server could cause OOM.

**Fix:** Add max iteration count and context deadline.

**Effort:** S (30 minutes)

---

### #63 — Missing ADRs for auth model and tenant attribution [MEDIUM]

**Dimension:** Governance
**Location:** `docs/decisions/` (only 4 ADRs)

No ADRs for: JWT auth model, tenant attribution fallback chain,
tiered pricing algorithm, reconciliation strategy. The tenant
attribution mapping is explicitly marked as uncertain in code comments.

**Fix:** Create ADRs for each decision.

**Effort:** M (2-3 days)

---

### #64 — CI lacks golangci-lint / staticcheck [MEDIUM]

**Dimension:** Governance
**Location:** `.github/workflows/ci.yml`

CI runs `go vet` and `govulncheck` but not `golangci-lint`. Misses
unchecked errors (e.g., swallowed `json.Marshal` in watcher),
inefficient assignments, and security issues.

**Fix:** Add `golangci-lint` with `errcheck`, `staticcheck`, `gosec`.

**Effort:** S (1 hour)

---

### #65 — Watcher backoff never resets after successful connection [MEDIUM]

**Dimension:** Operational robustness
**Location:** `internal/watcher/watcher.go:28-51`

Backoff escalates to 30s and never resets. A disconnect after hours
of stable connection still waits 30s to reconnect.

**Fix:** Reset `backoff = time.Second` after successful stream run.

**Effort:** S (minutes)

---

### #66–#72 — Low-severity findings

| # | Title | Location | Fix |
|---|-------|----------|-----|
| 66 | Hardcoded metering/rating intervals | `cmd/consumer/main.go:85,87` | Add env vars |
| 67 | Duplicated `thresholdLevels` | `rating.go:92` + `handler.go:443` | Export from rating |
| 68 | Rating skips unrated entries forever | `rating/rating.go:56-59` | Log missing rate name |
| 69 | Swallowed `json.Marshal` in watcher | `watcher.go:155,210,233` | Check errors |
| 70 | Containerfile Go version mismatch | `Containerfile:2` vs `go.mod:3` | Align versions |
| 71 | DurationMs truncation | `handler.go:351` | Use float64 duration |
| 72 | Inconsistent JSON response patterns | handler.go (various) | Accepted (PoC) |

## Priority Remediation Order

### Immediate (data integrity)

1. **#42** — Fix `InsertMeteringEntryBatch` missing `project_id` [S, minutes]
2. **#46** — Validate `DurationSeconds > 0` [S, minutes]
3. **#43** — Fix dashboard XSS (`textContent` instead of `innerHTML`) [S, 30 min]
4. **#44** — Guard concurrent reconciliation [S, 1 hour]

### Short-term (correctness under load)

5. **#57** — Transaction for metering + last_metered_at [S, 1 hour]
6. **#45** — Application-level event dedup [M, 1-2 days]
7. **#50** — Batch rating sweep (eliminate N+1) [M, 1 day]
8. **#51** — Replace LEFT JOIN with `rated_at IS NULL` [M, 1 day]
9. **#65** — Reset watcher backoff [S, minutes]
10. **#58** — Log NodeSets unmarshal failure [S, minutes]

### Medium-term (operational + security)

11. **#48** — Add watch stream read timeout [M, 1 day]
12. **#49** — Component health in `/readyz` [M, 1 day]
13. **#47** — Cross-tenant access check [M, 1 day]
14. **#54** — Remove wildcard CORS [S, minutes]
15. **#56** — Default debug dashboard to off [S, minutes]
16. **#53** — Fix CSV injection [S, 30 min]
17. **#55** — Add rate limiting [S, 1 hour]
18. **#59** — Invalidate project cache [S, 30 min]
19. **#60** — Bound OSAC pagination [S, 30 min]
20. **#64** — Add golangci-lint to CI [S, 1 hour]

### Backlog

21. **#52** — Test coverage for watcher/reconciler/authn/store [L, 1-2 weeks]
22. **#63** — Missing ADRs [M, 2-3 days]
23. **#66-72** — Low-severity items [S-M each]
24. v3 carry-forwards: #9, #13, #14, #23-26, #30-31, #34-35, #40

## Current State

| Category | Count |
|----------|-------|
| Total findings (all versions) | 72 |
| Fixed | 22 |
| Accepted (PoC) | 12 |
| Open (v1-v3 carry-forward) | 11 |
| **Open (new in v4)** | **27** |
| **Immediate priority** | **4 (#42, #43, #44, #46)** |

## Strengths

- **Excellent stress test infrastructure** — 850 req/s verified,
  documented, reproducible
- **Strong CI pipeline** — 3 workflows (basic, full-stack k3s, IPP
  gateway), all passing
- **IPP integration proven** — full ext_proc flow end-to-end in CI
- **Clean package structure** — well-separated concerns, good
  interface design
- **Comprehensive documentation** — CloudEvents catalog, deployment
  guides, ADRs, research docs
- **DISABLE_COMPONENTS** — clean operational toggle for testing
