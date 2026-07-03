# Adversarial Due Diligence Review — inventory-watcher (PR #9: Observability)

**Version:** 2.0 | **Date:** 2026-07-03 | **Reviewer:** AI-assisted
**Scope:** PR #9 — Prometheus metrics, K8s probes, structured logging, panic recovery, graceful shutdown
**Base:** [v1.0 review](adversarial-review-v1.md) (17 findings, 9 fixed, 3 accepted, 5 open)

---

## Executive Summary

PR #9 implements P1 and P2 from the observability plan, adding Prometheus
metrics on a separate port, Kubernetes liveness/readiness probes, structured
JSON logging, request logging with correlation IDs, panic recovery on all
goroutines, and graceful shutdown with a 30-second drain period.

This is a well-structured observability PR that closes the most critical
operational gaps from the v1 review. The architecture choices are sound:
separate metrics port (RHT pattern), `promauto` registration, `errgroup`
for goroutine lifecycle. The code is clean and follows Go idioms.

However, the review surfaces **one high-severity correctness bug** (panic
recovery in `safeGo` swallows the panic without propagating an error to
the errgroup, causing the process to silently continue with a dead
goroutine), **one high-severity cardinality issue** (`AlertsFiredTotal`
with unbounded `tenant_id` label), and several medium-severity design
issues that would cause problems at production scale.

**Overall assessment:** Strong observability addition. Two high-severity
issues should be fixed before merge; the rest are safe to defer.

---

## Scorecard (updated from v1)

| Dimension | v1 | v2 | Key change |
|-----------|----|----|------------|
| Security | ★★☆☆☆ | ★★★☆☆ | Auth added (pre-PR); probe endpoints properly exempted |
| Correctness | ★★★☆☆ | ★★★☆☆ | `safeGo` panic bug introduced; error handling improved elsewhere |
| Auditability | ★★★☆☆ | ★★★★☆ | Request IDs, structured logging, pipeline counters |
| Operational robustness | ★★☆☆☆ | ★★★★☆ | Probes, graceful shutdown, panic recovery, metrics |
| Performance | ★★★★☆ | ★★★★☆ | Minor overhead from double `statusWriter`; no regression |
| Design quality | ★★★★☆ | ★★★★☆ | Clean metrics package; some middleware layering issues |
| Maintainability | ★★★☆☆ | ★★★☆☆ | New code lacks tests for middleware/metrics; normalizePath fragile |
| Governance | ★★★★☆ | ★★★★☆ | Docs updated; still no CI pipeline |

---

## v1 Findings Verification

| v1 # | Title | v1 Status | v2 Status | Notes |
|---|---|---|---|---|
| 1 | No auth on API endpoints | Fixed | **Verified fixed** | JWT middleware added; probes correctly exempt |
| 2 | Silent error swallowing | Fixed | **Verified fixed** | Handlers return errors |
| 3 | Missing OSAC pagination | Fixed | **Verified fixed** | Offset/limit loop |
| 4 | Hardcoded default credentials | Accepted | Accepted | Unchanged |
| 5 | No HTTP server limits | Fixed | **Verified fixed** | ReadTimeout/WriteTimeout/MaxHeaderBytes set |
| 6 | Division by zero in rating | Fixed | **Verified fixed** | Guard added |
| 7 | Missing input validation | Fixed | **Verified fixed** | Validation added |
| 8 | Reconciler silent failures | Accepted | **Improved** | Reconciler now has drift metrics |
| 9 | No transaction boundaries | Open | Open | Unchanged |
| 10 | JSON injection in errors | Fixed | **Verified fixed** | `writeErrorJSON` helper |
| 11 | Scanner buffer size | Fixed | **Verified fixed** | 1MB buffer |
| 12 | N+1 query in summarizer | Fixed | **Verified fixed** | Batch lookup |
| 13 | Duplicate event constants | Open | Open | Unchanged |
| 14 | Unbounded slice allocation | Open | Open | Unchanged |
| 15 | No request IDs/tracing | Open | **Fixed by PR #9** | Request logger adds correlation ID |
| 16 | UTC timezone assumption | Accepted | Accepted | Unchanged |
| 17 | No CI pipeline | Open | Open | Unchanged |

---

## New Findings (PR #9)

### #18 — `safeGo` panic recovery silently kills goroutine

**Severity:** High | **Dimension:** Correctness | **Effort:** S

**Location:** `cmd/consumer/main.go:168-176`

**Description:** The `safeGo` wrapper recovers from panics and logs the error,
but returns `nil` to the errgroup. This means:

1. A panic in the watcher/reconciler/metering/rating goroutine is caught.
2. The errgroup receives `nil` (no error) — it thinks the goroutine exited
   normally.
3. The goroutine is dead but the process continues running with degraded
   functionality (e.g., no metering, no reconciliation).
4. No alert fires because the process is still alive (passes liveness probe).

The correct behavior is to return an error from the recovery handler so the
errgroup cancels all other goroutines and the process restarts.

```go
// Current (broken)
defer func() {
    if r := recover(); r != nil {
        logger.Error("goroutine panic", ...)
        // returns nil implicitly — errgroup doesn't know
    }
}()
return fn()

// Fixed
defer func() {
    if r := recover(); r != nil {
        logger.Error("goroutine panic", ...)
        err = fmt.Errorf("goroutine %s panicked: %v", name, r)
    }
}()
return fn()
```

**Risk:** A panic in any background goroutine (e.g., metering or rating) causes
that component to silently stop working. The service appears healthy (liveness
probe passes, readiness probe passes since the DB is still reachable), but no
new metering entries or cost entries are generated. Revenue data goes missing
with no visible alert.

**Recommendation:** Return an error from the deferred recovery so the errgroup
cancels context and the process exits. The Kubernetes restart policy will
restart the pod. Alternatively, add a metric for recovered panics and configure
an alert.

---

### #19 — Unbounded metric cardinality on `AlertsFiredTotal`

**Severity:** High | **Dimension:** Performance | **Effort:** S

**Location:** `internal/metrics/metrics.go:86-90`, `internal/rating/rating.go:138`

**Description:** `AlertsFiredTotal` uses `tenant_id` as a label. In a
multi-tenant system, each new tenant creates a new Prometheus time series.
The `threshold` label uses `fmt.Sprintf("%.0f", threshold)` which at least
produces a bounded set, but `tenant_id` is unbounded.

With 10,000 tenants, this creates 10,000+ time series for one metric alone.
Prometheus scrape time and memory usage grow linearly with cardinality. The
commonly accepted ceiling for a single metric is ~1,000 unique label
combinations.

**Risk:** At production scale, this metric causes Prometheus OOM or scrape
timeouts. Even before that, the `/metrics` endpoint response grows large
enough to cause slow scrapes and increased network traffic.

**Recommendation:** Remove `tenant_id` from the metric labels. Instead:
- Use a log line (already present) for tenant-specific correlation.
- If per-tenant alerting metrics are needed, use a bounded label like a
  tenant tier/category, or use a separate metric with exemplars.

---

### #20 — Middleware ordering hides panics from Prometheus metrics

**Severity:** High | **Dimension:** Correctness | **Effort:** S

**Location:** `cmd/consumer/main.go:140`

**Description:** The middleware chain is (outside-in):
```
RequestLogger → panicRecovery → HTTPMiddleware → auth → handler
```

`panicRecovery` sits **outside** `HTTPMiddleware`. When a handler panics,
the panic propagates upward through `HTTPMiddleware`'s
`next.ServeHTTP(sw, r)` call, bypassing the metric-recording code that
executes after `next.ServeHTTP` returns. `panicRecovery` catches the panic
and writes a 500, but `HTTPRequestsTotal` and `HTTPRequestDuration` never
record the request.

Additionally, both `RequestLogger` and `HTTPMiddleware` create their own
`statusWriter` wrapper, resulting in double wrapping:
- Two `time.Now()` calls and two duration calculations per request
- `http.Flusher`/`http.Hijacker` interfaces lost through the wrapper chain

**Risk:** If handlers panic frequently (e.g., nil pointer from DB pool issue),
Prometheus metrics show a traffic drop rather than an error spike. Alerting
on HTTP 500 rates misses the problem. Future streaming endpoints (SSE/WebSocket)
fail with confusing errors because `Flush()` is not available.

**Recommendation:**
1. Swap the middleware order so `panicRecovery` is **inside** `HTTPMiddleware`:
   `RequestLogger → HTTPMiddleware → panicRecovery → auth → handler`
2. Merge `RequestLogger` and `HTTPMiddleware` into a single middleware with
   one `statusWriter` to eliminate double wrapping and interface loss.
3. Add `Unwrap() http.ResponseWriter` to `statusWriter` for Go 1.20+
   `http.ResponseController` compatibility.

---

### #21 — Metrics server uses `Close()` instead of `Shutdown()`

**Severity:** Medium | **Dimension:** Operational robustness | **Effort:** S

**Location:** `cmd/consumer/main.go:152`

**Description:** The ingest server correctly uses `srv.Shutdown()` with a 30s
timeout for graceful drain, but the metrics server uses `metricsSrv.Close()`:

```go
// Metrics server shutdown (hard close):
g.Go(func() error {
    <-ctx.Done()
    return metricsSrv.Close()  // ← hard close, drops in-flight scrapes
})

// Ingest server shutdown (graceful):
g.Go(func() error {
    <-ctx.Done()
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()
    return srv.Shutdown(shutdownCtx)  // ← graceful drain
})
```

If Prometheus is in the middle of a `/metrics` scrape when the pod shuts
down, the connection is dropped and Prometheus records a scrape failure,
creating a gap in monitoring data.

**Risk:** Every pod restart causes a monitoring data gap. In a rolling
deployment, every pod restarts, potentially causing correlated scrape
failures across the fleet.

**Recommendation:** Use `Shutdown()` with a short timeout (5s is plenty for
a metrics scrape) on the metrics server as well.

---

### #22 — `/metrics` auth exemption on ingest port is unnecessary and widens attack surface

**Severity:** Medium | **Dimension:** Security | **Effort:** S

**Location:** `internal/authn/middleware.go:94-97`

**Description:** The auth middleware exempts `/metrics` from JWT validation:
```go
case "/healthz", "/readyz", "/metrics":
    next.ServeHTTP(w, r)
    return
```

However, the metrics endpoint is served on a separate port (`:9000`) with its
own HTTP server that has no auth middleware at all. The `/metrics` path
exemption in the ingest server's auth middleware is redundant — no handler is
registered for `/metrics` on the ingest server's mux.

While this exemption has no functional impact today (requests to
`ingest:8020/metrics` would get a 404 after bypassing auth), it creates a
risk: if someone later registers a handler at `/metrics` on the ingest mux
(e.g., for debugging), it would be unauthenticated by default.

**Risk:** Low immediate risk. Future maintenance risk — the exemption implies
`/metrics` should be available on the ingest port without auth.

**Recommendation:** Remove `/metrics` from the auth exemption list. Only
`/healthz` and `/readyz` need to be exempt on the ingest port.

---

### #23 — `normalizePath` will drift from actual routes

**Severity:** Medium | **Dimension:** Maintainability | **Effort:** S

**Location:** `internal/metrics/middleware.go:32-40`

**Description:** Path normalization for metrics uses hardcoded string prefix
checks:
```go
func normalizePath(path string) string {
    switch {
    case len(path) > len("/api/v1/quotas/") && path[:len("/api/v1/quotas/")] == "/api/v1/quotas/":
        return "/api/v1/quotas/{tenant_id}"
    case len(path) > len("/api/v1/customers/") && path[:len("/api/v1/customers/")] == "/api/v1/customers/":
        return "/api/v1/customers/{tenant_id}"
    default:
        return path
    }
}
```

Problems:
1. The `default` case returns the raw path. Any route not explicitly listed
   (e.g., `/api/v1/reports/costs`, `/api/v1/events`) passes through
   unchanged — this is fine for static routes but fragile.
2. If a new parameterized route is added (e.g., `/api/v1/resources/{id}`),
   someone must remember to update this function. There's no compile-time
   enforcement.
3. Uses manual string slicing instead of `strings.HasPrefix` — harder to read,
   same performance.

**Risk:** A new parameterized route added without updating `normalizePath`
creates cardinality explosion in HTTP request metrics (each unique parameter
value becomes a label).

**Recommendation:** Use `strings.HasPrefix` for readability. Add a code
comment linking this function to `ServeMux` route registration so future
developers know to update both. Consider using `r.Pattern` from Go 1.22+
instead of manual normalization (available since the project uses Go method
routing `"GET /healthz"`).

---

### #24 — `crypto/rand.Read` error ignored in `generateID`

**Severity:** Low | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/metrics/request_logger.go:32-36`

**Description:**
```go
func generateID() string {
    b := make([]byte, 8)
    rand.Read(b)
    return hex.EncodeToString(b)
}
```

`crypto/rand.Read` returns `(n int, err error)`. The error is discarded. If
the system entropy source is exhausted or unavailable (extremely rare on
Linux, possible in constrained containers), `Read` returns an error and `b`
contains zeroes, resulting in request ID `"0000000000000000"`.

**Risk:** Negligible in practice — `crypto/rand` on Linux reads from
`/dev/urandom` which never blocks. However, this is a Go best practice
violation that may fail linting.

**Recommendation:** Handle the error:
```go
if _, err := rand.Read(b); err != nil {
    return "unknown"
}
```
Or use `rand.Reader` with `io.ReadFull` for explicit error handling.

Note: As of Go 1.22, `crypto/rand.Read` is documented to always return
`len(b), nil` and panic on failure, making error handling technically
unnecessary. But the function signature still returns an error, and linters
flag discarded errors.

---

### #25 — Request ID not propagated via context to downstream handlers

**Severity:** Low | **Dimension:** Auditability | **Effort:** S

**Location:** `internal/metrics/request_logger.go:10-28`

**Description:** The `RequestLogger` middleware generates or extracts a
`request_id`, but only uses it in the final log line. It is not injected
into the request context or set as a response header. Downstream handlers
(event processing, metering, rating) cannot access the request ID for
their own log entries.

This means a single HTTP request generates:
- One access log line with `request_id`
- Multiple handler log lines (event processing, metering) without it

Correlating these requires matching timestamps, which is unreliable under
concurrent load.

**Risk:** Reduced observability under load — cannot trace a specific request
through the full processing pipeline.

**Recommendation:** Add the request ID to the request context and use it
in a logger that's passed to handlers:
```go
ctx := context.WithValue(r.Context(), requestIDKey, requestID)
w.Header().Set("X-Request-ID", requestID)
next.ServeHTTP(sw, r.WithContext(ctx))
```

---

### #26 — No tests for new middleware, panic recovery, or metrics instrumentation

**Severity:** Low | **Dimension:** Maintainability | **Effort:** M

**Location:** `internal/metrics/` (no test files), `cmd/consumer/main.go`

**Description:** The PR adds three new middleware layers (`HTTPMiddleware`,
`RequestLogger`, `panicRecovery`), a `safeGo` wrapper, a `parseLogLevel`
function, and a full metrics package — none of which have unit tests.

The only new tests are `TestLivenessProbe` and `TestReadinessProbe`, which
verify the probe handlers but not the infrastructure around them.

Untested code:
- `statusWriter.WriteHeader` — does it handle double calls correctly?
- `normalizePath` — does it handle edge cases (empty path, exact prefix
  match without trailing chars)?
- `panicRecovery` — does it return a proper 500 response with JSON body?
- `safeGo` — does it log the panic stack trace correctly?
- `parseLogLevel` — does it handle mixed case, empty string?
- Metrics — are counters incremented correctly on each code path?

**Risk:** Regressions go undetected. The middleware is on the critical
request path for every API call.

**Recommendation:** Add unit tests for at least:
1. `statusWriter` — verifies status capture and double-write behavior
2. `normalizePath` — verifies all known routes and edge cases
3. `panicRecovery` — verifies 500 response on panic
4. `parseLogLevel` — trivial test, documents behavior

---

### #27 — Resource gauges only updated by reconciler (hourly)

**Severity:** Low | **Dimension:** Operational robustness | **Effort:** S

**Location:** `internal/metrics/metrics.go:96-118`, `internal/reconciler/reconciler.go`

**Description:** `LiveComputeInstances`, `LiveClusters`, and `LiveModels`
gauges are only set during reconciliation, which runs every hour by default.
Between reconciliation runs, the gauges show stale data.

If a burst of new VMs is created, the dashboard shows the old count for up
to 60 minutes. Conversely, if VMs are deleted, the dashboard overstates
capacity.

**Risk:** Dashboard shows misleading resource counts. Operators make
decisions based on stale gauge data.

**Recommendation:** Accept for PoC. In production, either reduce the
reconcile interval or update gauges in the watcher (event-driven path)
as well.

---

### #28 — `normalizePath` 404 path creates cardinality attack vector

**Severity:** Medium | **Dimension:** Security | **Effort:** S

**Location:** `internal/metrics/middleware.go:33-40`

**Description:** The `default` case in `normalizePath` returns the raw path.
An attacker can send requests to random paths (`/x1`, `/x2`, ..., `/x100000`)
which all return 404 but each creates a unique Prometheus time series in
`HTTPRequestsTotal` (keyed by method + path + status).

This is distinct from #23 (fragility for known routes) — this is an
externally-exploitable cardinality bomb via paths that don't match any route.

**Risk:** An attacker sends a few thousand requests to unique paths. Each
creates a new time series. Prometheus memory grows unboundedly, eventually
causing OOM or making scrapes fail.

**Recommendation:** Add a catch-all that maps unknown paths to a single bucket:
```go
default:
    switch path {
    case "/api/v1/events", "/api/v1/reports/costs", "/api/v1/reports/summary",
         "/api/v1/debug/config", "/healthz", "/readyz", "/debug/dashboard", "/":
        return path
    default:
        return "/other"
    }
```

---

### #29 — Probe requests generate INFO-level log noise

**Severity:** Low | **Dimension:** Operational robustness | **Effort:** S

**Location:** `internal/metrics/request_logger.go:22-28`

**Description:** Every Kubernetes probe call (`/healthz` every 10s, `/readyz`
every 10s) goes through `RequestLogger` and produces an INFO-level log line.
At 12 log lines per minute per pod, in a 10-pod deployment this is 120 probe
log lines per minute — noise that obscures real signals during incident
investigation.

**Risk:** Log aggregation costs increase. Operator searching for errors
during an incident has to wade through probe noise.

**Recommendation:** Either exclude probe paths from request logging, or
log them at DEBUG level:
```go
level := slog.LevelInfo
if path == "/healthz" || path == "/readyz" {
    level = slog.LevelDebug
}
logger.Log(r.Context(), level, "http request", ...)
```

---

### #30 — `LiveModels` gauge defined but never set

**Severity:** Low | **Dimension:** Operational robustness | **Effort:** S

**Location:** `internal/metrics/metrics.go:96-118`

**Description:** The `LiveModels` gauge is declared in `metrics.go` but is
never updated anywhere in the codebase. The reconciler updates
`LiveComputeInstances` and `LiveClusters`, but MaaS models arrive via
event ingestion, not reconciliation. No equivalent update exists in the
MaaS event handler.

Similarly, no `LiveBareMetalInstances` gauge exists, and the
`reconcileBareMetalInstances` function lacks metric instrumentation.

**Risk:** A dashboard showing `cost_consumer_live_models` displays 0
at all times, even while MaaS events are being processed. Operators
either waste time investigating or learn to distrust the gauge.

**Recommendation:** Either set `LiveModels` in the MaaS event handler,
or remove the gauge until it can be properly populated. Add missing
instrumentation to `reconcileBareMetalInstances`.

---

### #31 — No error-rate metrics for sweep failures

**Severity:** Low | **Dimension:** Auditability | **Effort:** S

**Location:** `internal/metering/metering.go:47-53`, `internal/rating/rating.go:41-43`

**Description:** When `BillableComputeInstances()` or `UnratedMeteringEntries()`
fail, the code logs and returns early. No Prometheus counter is incremented.
The `MeteringSweepDuration` histogram only records successful sweeps (the
`Observe` call is never reached on failure).

**Risk:** Database intermittently unreachable. Every metering sweep fails.
The `MeteringSweepDuration` histogram shows no new samples — indistinguishable
from "no resources to meter." No Prometheus-based alert for sweep failures.

**Recommendation:** Add `metering_sweep_errors_total` and
`rating_sweep_errors_total` counters, incremented on each query failure.

---

### #32 — `panicRecovery` sends JSON body with text/plain Content-Type

**Severity:** Informational | **Dimension:** Correctness | **Effort:** S

**Location:** `cmd/consumer/main.go:184`

**Description:** `http.Error()` sets `Content-Type: text/plain` but the body
is JSON: `{"error":"internal server error"}`. Clients checking Content-Type
before parsing will not treat this as JSON.

**Recommendation:** Use direct header/body writing:
```go
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusInternalServerError)
w.Write([]byte(`{"error":"internal server error"}`))
```

---

### #33 — `fmt` import in `rating.go` breaks goimports grouping

**Severity:** Informational | **Dimension:** Maintainability | **Effort:** S

**Location:** `internal/rating/rating.go:8-10`

**Description:** The `fmt` import is placed between `time` and the project
imports, breaking the standard Go import grouping (stdlib, then third-party,
then project):

```go
import (
    "log/slog"
    "time"

    "fmt"  // ← should be in the stdlib group above

    "github.com/osac-project/cost-event-consumer/internal/inventory"
    "github.com/osac-project/cost-event-consumer/internal/metrics"
)
```

**Recommendation:** Move `"fmt"` into the stdlib group with `"log/slog"` and
`"time"`.

---

## Findings Status Summary (All Findings, v1 + v2)

| # | Title | Severity | Dimension | Status |
|---|-------|----------|-----------|--------|
| 1 | No auth on API endpoints | Critical | Security | Fixed |
| 2 | Silent error swallowing | Critical | Correctness | Fixed |
| 3 | Missing OSAC pagination | Critical | Correctness | Fixed |
| 4 | Hardcoded default credentials | High | Security | Accepted (PoC) |
| 5 | No HTTP server limits | High | Operational | Fixed |
| 6 | Division by zero in rating | High | Correctness | Fixed |
| 7 | Missing input validation | High | Security | Fixed |
| 8 | Reconciler silent failures | Medium | Correctness | Accepted (PoC) |
| 9 | No transaction boundaries | Medium | Correctness | Open |
| 10 | JSON injection in errors | Medium | Security | Fixed |
| 11 | Scanner buffer size | Medium | Operational | Fixed |
| 12 | N+1 query in summarizer | Medium | Performance | Fixed |
| 13 | Duplicate event constants | Low | Maintainability | Open |
| 14 | Unbounded slice allocation | Low | Performance | Open |
| 15 | No request IDs/tracing | Low | Auditability | **Fixed (PR #9)** |
| 16 | UTC timezone assumption | Info | Correctness | Accepted |
| 17 | No CI pipeline | Info | Governance | Open |
| **18** | **`safeGo` silently kills goroutine** | **High** | **Correctness** | **Open** |
| **19** | **Unbounded `tenant_id` metric label** | **High** | **Performance** | **Open** |
| **20** | **Middleware ordering hides panics from metrics** | **High** | **Correctness** | **Open** |
| **21** | **Metrics server hard close** | **Medium** | **Operational** | **Open** |
| **22** | **Unnecessary `/metrics` auth exemption** | **Medium** | **Security** | **Open** |
| **23** | **`normalizePath` fragility** | **Medium** | **Maintainability** | **Open** |
| **24** | **`rand.Read` error ignored** | **Low** | **Correctness** | **Open** |
| **25** | **Request ID not in context** | **Low** | **Auditability** | **Open** |
| **26** | **No middleware tests** | **Low** | **Maintainability** | **Open** |
| **27** | **Stale resource gauges** | **Low** | **Operational** | **Open** |
| **28** | **404 path cardinality attack** | **Medium** | **Security** | **Open** |
| **29** | **Probe log noise** | **Low** | **Operational** | **Open** |
| **30** | **`LiveModels` gauge never set** | **Low** | **Operational** | **Open** |
| **31** | **No sweep error metrics** | **Low** | **Auditability** | **Open** |
| **32** | **Panic response Content-Type** | **Info** | **Correctness** | **Open** |
| **33** | **Import grouping** | **Info** | **Maintainability** | **Open** |

---

## Priority Remediation Order (PR #9 findings only)

| Priority | Finding | Effort | Why now |
|---|---|---|---|
| 1 | #18 `safeGo` panic bug | S | Goroutine death with no recovery — invisible data loss |
| 2 | #20 Middleware ordering | S | Panics invisible to Prometheus — single-line swap |
| 3 | #19 `tenant_id` cardinality | S | Prometheus OOM at scale |
| 4 | #28 404 path cardinality | S | Externally exploitable via random paths |
| 5 | #21 Metrics server shutdown | S | Clean deploys |
| 6 | #22 `/metrics` auth exemption | S | Remove dead code |
| 7 | #23 `normalizePath` readability | S | Use `strings.HasPrefix` + comment |
| 8 | #25 Request ID context | S | Enables full request tracing |
| 9 | #24 `rand.Read` error | S | Linter compliance |
| 10 | #26 Middleware tests | M | Test coverage on critical path |
| 11 | #29-#33 Low/Info findings | S-M | Accept for PoC, fix before production |

**Recommendation:** Fix #18, #19, #20, and #28 before merge. The rest can
be follow-up. All four are small fixes (1-5 lines each).

---

## Accepted Risks

| # | Finding | Rationale |
|---|---------|-----------|
| 4 | Default credentials | PoC only; matches dev Docker containers |
| 8 | Reconciler silent failures | Safety net, not primary data path; improved by drift metrics |
| 16 | UTC assumption | Correct for billing; documented |
| 27 | Stale resource gauges | Acceptable for PoC; reconciler runs hourly |

---

## Strengths (What's Done Well in PR #9)

- **Separate metrics port** — follows RHT pattern (Koku, chrome-service-backend).
  No auth on metrics, no path conflicts with API endpoints.
- **Clean metric naming** — `cost_consumer_` namespace, consistent with
  Prometheus naming conventions. Good use of `promauto` for registration.
- **Graceful shutdown** — `srv.Shutdown()` with 30s drain is correct for
  HTTP servers. Errgroup cancellation propagates shutdown across all goroutines.
- **Panic recovery on all goroutines** — both HTTP handlers and background
  goroutines are protected. Stack traces are logged.
- **Structured logging options** — `LOG_FORMAT=json` for OpenShift log
  aggregation is the right pattern. `LOG_LEVEL` correctly wired.
- **Readiness probe pings DB** — correctly returns 503 when the database is
  unreachable, preventing traffic routing to unhealthy pods.
- **Documentation updated** — observability plan, API reference, and
  implementation status all updated to match the code.

---

## Current State

| Category | Count |
|---|---|
| Total findings (all versions) | 33 |
| Fixed (v1 + v2) | 10 |
| Accepted | 4 |
| Open (v1 carry-forward) | 4 (#9, #13, #14, #17) |
| Open (new in v2) | 15 (#18–#33) |
| **Must fix before merge** | **4 (#18, #19, #20, #28)** |
