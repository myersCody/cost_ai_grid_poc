# Adversarial Due Diligence Review — REQ-13 Custom Metrics (PR #16)

**Version:** 3.0 | **Date:** 2026-07-03 | **Reviewer:** AI-assisted
**Scope:** PR #16 — Config-driven custom metric extraction from CloudEvents
**Base:** [v2.0 review](adversarial-review-v2.md) (33 findings; 4 must-fix resolved)

---

## Executive Summary

PR #16 implements REQ-13: the ability to meter arbitrary numeric fields from
CloudEvents without code changes. An operator drops a JSON config file
(via `CUSTOM_METRICS_CONFIG` env var / ConfigMap), and the ingest handler
automatically extracts configured fields into `metering_entries`. Rating,
reporting, and quotas all work automatically because the downstream pipeline
already operates on free-text `(resource_type, meter_name)` pairs.

The design is clean and pragmatic. The `custommetrics` package is well-isolated
behind a `MeteringStore` interface, has 24 unit tests and 2 integration tests
with PostgreSQL, and hooks into the handler's `default` branch so hardcoded
handlers are untouched. The config validation catches most misconfiguration
at startup.

The review surfaces **no critical or high-severity issues**. There are
**three medium-severity** findings (partial string parsing in `toFloat64`,
negative value acceptance, config-driven metric cardinality) and several
low-severity design and maintainability items. Overall this is a solid
implementation ready for merge.

---

## Scorecard (updated from v2)

| Dimension | v2 | v3 | Key change |
|-----------|----|----|------------|
| Security | ★★★☆☆ | ★★★☆☆ | No new attack surface; config is operator-only |
| Correctness | ★★★☆☆ | ★★★★☆ | v2 must-fix items resolved; `toFloat64` partial parse is new |
| Auditability | ★★★★☆ | ★★★★☆ | Good debug logging on custom metric processing |
| Operational robustness | ★★★★☆ | ★★★★☆ | Unchanged; config loaded at startup only |
| Performance | ★★★★☆ | ★★★★☆ | Double unmarshal is minor; no unbounded allocations |
| Design quality | ★★★★☆ | ★★★★★ | Clean interface segregation, minimal coupling |
| Maintainability | ★★★☆☆ | ★★★★☆ | 26 tests covering core paths; good separation |
| Governance | ★★★★☆ | ★★★★☆ | Design doc, updated implementation status |

---

## v2 Findings Verification

| v2 # | Title | v2 Status | v3 Status | Notes |
|---|---|---|---|---|
| 18 | `safeGo` silently kills goroutine | Open | **Fixed** | Returns error to errgroup |
| 19 | Unbounded `tenant_id` metric label | Open | **Fixed** | Label removed |
| 20 | Middleware ordering hides panics | Open | **Fixed** | Reordered |
| 21 | Metrics server hard close | Open | **Fixed** | Uses `Shutdown()` |
| 22 | `/metrics` auth exemption | Open | **Fixed** | Removed |
| 28 | 404 path cardinality attack | Open | **Fixed** | Unknown paths → `/other` |
| 29 | Probe log noise | Open | **Fixed** | Demoted to DEBUG |
| 32 | Panic response Content-Type | Open | **Fixed** | Sets `application/json` |
| 33 | Import grouping | Open | **Fixed** | Stdlib group fixed |

All 4 must-fix items from v2 are verified resolved.

---

## New Findings (PR #16)

### #34 — `toFloat64` partial-parses strings via `fmt.Sscanf`

**Severity:** Medium | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/custommetrics/custommetrics.go:248-251`

**Description:** `fmt.Sscanf(n, "%f", &f)` parses the leading numeric portion
of a string and ignores trailing garbage. The string `"123abc"` parses as
`123.0` with no error. The string `"NaN"` parses as `NaN`. The string
`"Inf"` parses as `+Inf`.

```go
case string:
    var f float64
    _, err := fmt.Sscanf(n, "%f", &f)
    return f, err == nil
```

**Risk:** A malformed event with a string field like `"gpu_seconds": "3600xyz"`
silently meters 3600 instead of being rejected. `NaN` or `Inf` values
would flow into the database and produce nonsensical cost calculations
(any cost × NaN = NaN, any cost × Inf = Inf).

**Recommendation:** Use `strconv.ParseFloat(n, 64)` instead — it rejects
trailing garbage and returns errors for NaN/Inf strings. Also add a guard
against NaN/Inf in the float64 branch:
```go
case float64:
    if math.IsNaN(n) || math.IsInf(n, 0) {
        return 0, false
    }
    return n, true
```

---

### #35 — Negative meter values silently accepted

**Severity:** Medium | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/custommetrics/custommetrics.go:187-189`

**Description:** The zero-value check (`if value == 0 { continue }`) skips
zero values, but negative values pass through and are inserted as metering
entries. A CloudEvent with `"gpu_compute_seconds": -3600.0` would create
a metering entry with `value = -3600.0`.

**Risk:** Negative metering entries produce negative cost entries in the
rating sweep. This could be used (accidentally or intentionally) to
reduce a tenant's billed cost. The hardcoded VM/cluster meters cannot
produce negative values because they derive from `time.Since()`, but
custom metrics accept raw values from external events.

**Recommendation:** Skip negative values alongside zero:
```go
if value <= 0 {
    continue
}
```
Or log a warning and skip, depending on whether negative corrections
are a valid business concept.

---

### #36 — Config-driven metric cardinality risk via `resource_type`

**Severity:** Medium | **Dimension:** Performance | **Effort:** S

**Location:** `internal/custommetrics/custommetrics.go:27`, `internal/metrics/metrics.go:31-39`

**Description:** The `MeteringEntriesCreated` Prometheus counter uses
`resource_type` and `meter_name` as labels. Both values come from the
custom metrics config file. An operator can define arbitrary
`resource_type` values (e.g., one per GPU SKU: `gpu_a100`, `gpu_h100`,
`gpu_l40s`, ...). Each unique `(resource_type, meter_name)` combination
creates a new time series.

This is less severe than the `tenant_id` cardinality issue (#19) because
config values are operator-controlled and bounded by the number of
definitions in the config file. But it's worth noting that the config
file has no limit on the number of definitions.

**Risk:** An overly detailed config with 100+ resource types creates
~200+ time series for this single metric. Unlikely to cause OOM but
could make dashboards noisy.

**Recommendation:** Accept for PoC. Document that `resource_type` should
be a broad category (e.g., `gpu_instance`), not a per-SKU identifier.

---

### #37 — Double JSON unmarshal of event data

**Severity:** Low | **Dimension:** Performance | **Effort:** S

**Location:** `internal/api/handler.go:157-160`, `internal/custommetrics/custommetrics.go:151-154`

**Description:** When a custom metric event is received, `ce.Data` is
unmarshaled into `map[string]interface{}` twice:
1. In `handleEvent` (line 158) for `ClassifyEvent` — extracts resource/tenant IDs
2. In `ProcessEvent` (line 152) for meter value extraction

**Risk:** Negligible performance impact — JSON unmarshal of a typical
CloudEvent data payload (<1KB) takes <10μs. But it's unnecessary work.

**Recommendation:** Accept for PoC. If optimizing later, pass the already-
parsed map from `ClassifyEvent` into `ProcessEvent` instead of raw bytes.

---

### #38 — Classify fallback overwrites partial hardcoded results

**Severity:** Low | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/api/handler.go:155-161`

**Description:** The fallback logic checks `resourceID == "" || tenantID == ""`.
If `classifyEvent` returns a valid `resourceID` but empty `tenantID`, the
code falls through to `ClassifyEvent` which reassigns all three variables:
```go
resourceType, resourceID, tenantID = h.customMetrics.ClassifyEvent(...)
```

The previously-valid `resourceID` from `classifyEvent` is overwritten
with whatever the custom metric extracts (which might be different or empty).

**Risk:** In practice, if `classifyEvent` returns partial results for a
known event type, the custom metrics fallback won't trigger (because
`HasEventType` returns false for known types). This is only reachable if
a future refactoring adds a known type that can produce partial IDs. Low
probability.

**Recommendation:** Accept for PoC. The current handler switch structure
prevents this from being reachable.

---

### #39 — No hot-reload of custom metrics config

**Severity:** Low | **Dimension:** Operational robustness | **Effort:** M

**Location:** `cmd/consumer/main.go:132-140`

**Description:** The custom metrics config file is loaded once at startup.
Changing the config requires a pod restart. In Kubernetes, changing a
ConfigMap does not automatically restart pods (unless using a sidecar
or controller).

**Risk:** An operator adds a new custom metric definition, updates the
ConfigMap, and expects it to take effect. Nothing happens until the pod
is restarted. No warning is logged.

**Recommendation:** Accept for PoC. For production, either:
- Add a file watcher (fsnotify) to reload on change
- Document that a `kubectl rollout restart` is required after config changes

---

### #40 — Shadow warning doesn't tell operator the definition is inert

**Severity:** Low | **Dimension:** Maintainability | **Effort:** S

**Location:** `internal/custommetrics/custommetrics.go:73-76`

**Description:** When a custom metric `event_type` matches a hardcoded
handler, a warning is logged:
```
"custom metric event_type shadows a hardcoded handler"
```

But this doesn't explain that the custom definition will never execute
because the `switch` statement matches hardcoded types before the
`default` branch. The operator may think their custom definition
overrides the built-in behavior.

**Risk:** Operator confusion. They configure a custom definition for
`osac.compute_instance.lifecycle` expecting different metering behavior,
but the hardcoded handler always runs.

**Recommendation:** Change the warning message to:
```
"custom metric event_type shadows a hardcoded handler — the built-in
handler will always take precedence; this definition will be ignored"
```

---

### #41 — `extractField` only strips one `data.` prefix

**Severity:** Informational | **Dimension:** Correctness | **Effort:** S

**Location:** `internal/custommetrics/custommetrics.go:212`

**Description:** `strings.TrimPrefix(path, "data.")` strips at most one
leading `data.` segment. If the actual JSON field is literally named
`data` (e.g., `{"data": {"foo": 1}}`), the path `data.data.foo` becomes
`data.foo`, which would correctly traverse `data` → `foo`. But `data.foo`
would strip to `foo`, bypassing the `data` wrapper.

**Risk:** Confusing behavior if someone has a field literally named `data`
inside the data payload. Unlikely in practice — the `data.` prefix
stripping is a convenience for config authors who think of the CloudEvents
`data` wrapper as part of the path.

**Recommendation:** Document this behavior in the config format docs.

---

## Findings Status Summary (All Findings, v1 + v2 + v3)

| # | Title | Severity | Status |
|---|-------|----------|--------|
| 1 | No auth on API endpoints | Critical | Fixed |
| 2 | Silent error swallowing | Critical | Fixed |
| 3 | Missing OSAC pagination | Critical | Fixed |
| 4 | Hardcoded default credentials | High | Accepted (PoC) |
| 5 | No HTTP server limits | High | Fixed |
| 6 | Division by zero in rating | High | Fixed |
| 7 | Missing input validation | High | Fixed |
| 8 | Reconciler silent failures | Medium | Accepted (PoC) |
| 9 | No transaction boundaries | Medium | Open |
| 10 | JSON injection in errors | Medium | Fixed |
| 11 | Scanner buffer size | Medium | Fixed |
| 12 | N+1 query in summarizer | Medium | Fixed |
| 13 | Duplicate event constants | Low | Open |
| 14 | Unbounded slice allocation | Low | Open |
| 15 | No request IDs/tracing | Low | Fixed |
| 16 | UTC timezone assumption | Info | Accepted |
| 17 | No CI pipeline | Info | **Fixed** |
| 18 | `safeGo` silently kills goroutine | High | **Fixed** |
| 19 | Unbounded `tenant_id` metric label | High | **Fixed** |
| 20 | Middleware ordering hides panics | High | **Fixed** |
| 21 | Metrics server hard close | Medium | **Fixed** |
| 22 | `/metrics` auth exemption | Medium | **Fixed** |
| 23 | `normalizePath` fragility | Medium | Partially fixed |
| 24 | `rand.Read` error ignored | Low | Open |
| 25 | Request ID not in context | Low | Open |
| 26 | No middleware tests | Low | **Fixed** |
| 27 | Stale resource gauges | Low | Accepted (PoC) |
| 28 | 404 path cardinality attack | Medium | **Fixed** |
| 29 | Probe log noise | Low | **Fixed** |
| 30 | `LiveModels` gauge never set | Low | Open |
| 31 | No sweep error metrics | Low | Open |
| 32 | Panic response Content-Type | Info | **Fixed** |
| 33 | Import grouping | Info | **Fixed** |
| **34** | **`toFloat64` partial string parse** | **Medium** | **Fixed** |
| **35** | **Negative meter values accepted** | **Medium** | **Fixed** |
| **36** | **Config-driven metric cardinality** | **Medium** | **Accepted (PoC)** |
| **37** | **Double JSON unmarshal** | **Low** | **Accepted (PoC)** |
| **38** | **Classify fallback overwrites** | **Low** | **Accepted (PoC)** |
| **39** | **No config hot-reload** | **Low** | **Accepted (PoC)** |
| **40** | **Shadow warning unclear** | **Low** | **Fixed** |
| **41** | **`data.` prefix stripping** | **Info** | **Accepted** |

---

## Priority Remediation Order (PR #16 findings only)

| Priority | Finding | Effort | Why |
|---|---|---|---|
| 1 | #34 `toFloat64` partial parse | S | NaN/Inf values corrupt cost data |
| 2 | #35 Negative values | S | One-liner guard, prevents negative costs |
| 3 | #40 Shadow warning text | S | One-line string change |
| 4 | #36-#39, #41 | — | Accept for PoC |

**Recommendation:** Fix #34 and #35 before merge — both are one-liner
fixes that prevent garbage data from entering the pipeline. #40 is a
nice-to-have. The rest are fine for PoC.

---

## Strengths (What's Done Well in PR #16)

- **Clean interface segregation** — `MeteringStore` interface limits the
  custom metrics package to a single method. No coupling to the full store.
- **Hardcoded handlers untouched** — custom metrics only run in the `default`
  branch. Zero risk of regressing existing behavior.
- **Config validation at startup** — malformed config fails fast with clear
  error messages. No runtime surprises from missing fields.
- **Comprehensive test coverage** — 24 unit tests cover parsing, validation,
  extraction, type coercion, edge cases (nil registry, missing fields, zero
  values, no duration). 2 integration tests verify end-to-end with PostgreSQL.
- **Design doc** — `docs/research/req13-custom-metrics-design.md` documents
  the approach, alternatives considered, and rationale.
- **Minimal footprint** — 267 lines of implementation, stdlib only, no new
  dependencies. The entire feature is removable by deleting one package.

---

## Current State

| Category | Count |
|---|---|
| Total findings (all versions) | 41 |
| Fixed | 19 |
| Accepted | 8 |
| Open (carry-forward) | 8 (#9, #13, #14, #17, #23, #24, #25, #26, #30, #31) |
| Open (new in v3) | 3 (#34, #35, #40) |
| **Should fix before merge** | **2 (#34, #35)** |
