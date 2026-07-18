# REQ-13: Config-Driven Custom Metric Extraction

> **Status:** Implemented and merged. The design below was built as described.

## Context

**Jira:** [COST-3549](https://redhat.atlassian.net/browse/COST-3549) —
"Metric-based custom cost distribution" (status: Refinement, 10+ customer
requests since 2023). Direction: custom rates based on CloudEvents,
configuration-driven. Equation-based pricing (like Monetize 360) suggested
but deferred to post-PoC.

**Key use case from COST-3549:** OpenShift AI cost distribution — "distribute
costs across N users using custom metrics: number_of_users,
number_of_prompts_per_user, number_of_generated_words." This maps directly
to what we're building: CloudEvent fields → configurable meters → rated
costs per tenant.

**Related approaches referenced in COST-3549:**
- CloudKitty PyScript (OpenStack) — users define metrics via Python scripting
- CloudKitty Hashmap — equivalent to our price lists / rate tables
- GoRules/Zen — JSON Decision Model with visual editor (see Phase 2 below)
- Monetize 360 — equation-based pricing with parameters

For the PoC (Phase 1), direct field extraction from CloudEvents covers all
current use cases. GoRules/Zen is the Phase 2 path for complex rating logic.

Today every meter name is hardcoded — event handlers in `handler.go` explicitly
extract fields and create specific `MeteringEntry` rows. Adding a new metric
dimension (e.g. `gpu_memory_gib_seconds`) requires Go code changes and a
redeploy. REQ-13 asks for configurable extraction so new dimensions can be
added via a JSON config file, which in OpenShift comes from a ConfigMap mount.

The rating engine is already generic — `FindRate` matches on
`(resource_type, meter_name)` and `ApplyRate` handles flat or tiered pricing.
If a metering entry exists and a matching rate row exists, cost flows
automatically. **Only the ingestion/extraction side needs changes.**

## Approach

New package `internal/custommetrics/` with a `Registry` that loads extraction
rules from a JSON file at startup. Hooks into `handleEvent`'s default case —
hardcoded handlers (VM, cluster, MaaS) continue working unchanged, custom
metrics are additive.

### Example: CloudEvent → Config → Metering Entries

**1. The incoming CloudEvent** (POST to `/api/v1/events`):

```json
{
  "id": "evt-gpu-001",
  "type": "osac.gpu.lifecycle",
  "source": "osac/region-eu-1",
  "time": "2026-07-03T10:00:00Z",
  "data": {
    "instance_id": "gpu-i-abc123",
    "tenant_id": "tenant-acme",
    "gpu_model": "A100",
    "gpu_memory_gib_seconds": 245760.0,
    "gpu_compute_seconds": 3600.0,
    "duration_seconds": 3600,
    "state": "RUNNING"
  }
}
```

**2. The custom metrics config** that tells the system how to extract meters:

```json
{
  "custom_metrics": [
    {
      "event_type": "osac.gpu.lifecycle",
      "resource_type": "gpu_instance",
      "resource_id_field": "instance_id",
      "tenant_id_field": "tenant_id",
      "meters": [
        {
          "meter_name": "gpu_memory_gib_seconds",
          "value_field": "gpu_memory_gib_seconds",
          "unit": "gib_seconds"
        },
        {
          "meter_name": "gpu_compute_seconds",
          "value_field": "gpu_compute_seconds",
          "unit": "seconds"
        }
      ]
    }
  ]
}
```

How the config fields map to the CloudEvent:

```
CloudEvent envelope              Config field              Used for
─────────────────────             ────────────              ────────
type: "osac.gpu.lifecycle"   ←→   event_type               Route to this config entry
                                  resource_type             "gpu_instance" (written to metering_entries)
data.instance_id             ←→   resource_id_field         Extracted as resource_id
data.tenant_id               ←→   tenant_id_field           Extracted as tenant_id
data.gpu_memory_gib_seconds  ←→   meters[0].value_field     Extracted as meter value
data.gpu_compute_seconds     ←→   meters[1].value_field     Extracted as meter value
data.duration_seconds              (auto-detected)          period_start = time - duration
```

**3. The resulting metering entries** (inserted into `metering_entries` table):

| resource_type | resource_id | tenant_id | meter_name | value | unit | period_start | period_end |
|---|---|---|---|---|---|---|---|
| `gpu_instance` | `gpu-i-abc123` | `tenant-acme` | `gpu_memory_gib_seconds` | 245760.0 | `gib_seconds` | 2026-07-03T09:00:00Z | 2026-07-03T10:00:00Z |
| `gpu_instance` | `gpu-i-abc123` | `tenant-acme` | `gpu_compute_seconds` | 3600.0 | `seconds` | 2026-07-03T09:00:00Z | 2026-07-03T10:00:00Z |

**4. To price these**, add matching rows to the `rates` table (manually or via seed):

```sql
INSERT INTO rates (resource_type, meter_name, cost_type, price_per_unit, currency)
VALUES ('gpu_instance', 'gpu_memory_gib_seconds', 'Infrastructure', 0.000005, 'USD'),
       ('gpu_instance', 'gpu_compute_seconds',    'Infrastructure', 0.02,     'USD');
```

The existing rating sweep picks them up automatically — no code changes.

**Nested field example:** If the CloudEvent data had `"spec": {"gpu_count": 4}`,
the config would use `"value_field": "spec.gpu_count"` (dot notation walks
into nested objects).

Field paths are always relative to the CloudEvent `data` object.

## Changes

### New: `internal/custommetrics/custommetrics.go`

Types:
- `CustomMetricsConfig` — top-level with `CustomMetrics []CustomMetricDef`
- `CustomMetricDef` — `EventType`, `ResourceType`, `ResourceIDField`,
  `TenantIDField`, `Meters []MeterDef`
- `MeterDef` — `MeterName`, `ValueField`, `Unit`
- `Registry` — holds `map[string]CustomMetricDef` keyed by event type

Functions:
- `LoadFromFile(path, logger)` — reads JSON, validates, returns Registry.
  Returns nil Registry (not error) if path is empty, making the feature opt-in.
- `(r *Registry) HasEventType(string) bool`
- `(r *Registry) ClassifyEvent(eventType, data) (resourceType, resourceID, tenantID)`
- `(r *Registry) ProcessEvent(ctx, store, eventType, data, eventTime, logger) error`
  — unmarshals data to `map[string]interface{}`, walks configured field paths,
  calls `store.InsertMeteringEntry` for each meter with value > 0
- `extractField(data map[string]interface{}, path string)` — dot-notation walker
- `toFloat64(interface{})` / `toString(interface{})` — type coercion helpers
- `validate(cfg)` — checks required fields, rejects event types that collide
  with hardcoded constants

Period calculation: `period_end = event_time`. If a `duration_seconds` field
exists in the data, `period_start = event_time - duration`. Otherwise
`period_start = period_end` (point-in-time event).

No new Go dependencies — uses `encoding/json`, `os`, `strings` from stdlib.

### New: `internal/custommetrics/custommetrics_test.go`

Unit tests (no database):
- Config parsing (valid, empty path, invalid JSON, missing required fields)
- Field extraction (flat, nested, missing field, wrong type)
- `toFloat64` conversions (`float64`, `json.Number`, `int`, `string`)
- `ClassifyEvent` extracts resource_id and tenant_id
- `ProcessEvent` with a mock/interface store

### Modified: `internal/config/config.go`

Add one field to `Config`:
```go
CustomMetricsConfigPath string
```
Load from `CUSTOM_METRICS_CONFIG` env var. Add to `DiagnosticInfo`.

### Modified: `internal/ingest/handler.go`

A) Add `customMetrics *custommetrics.Registry` field to `Handler` struct.

B) Update `NewHandler` signature to accept the registry.

C) In `handleEvent`, replace the `default` case:
```go
default:
    if h.customMetrics != nil && h.customMetrics.HasEventType(ce.Type) {
        processingErr = h.customMetrics.ProcessEvent(ctx, h.store, ce.Type, ce.Data, ce.Time, h.logger)
    } else {
        h.logger.Warn("unknown CloudEvent type", "type", ce.Type)
    }
```

D) After `classifyEvent` returns empty resourceID, add fallback to custom
   registry before returning the 400 error:
```go
if resourceID == "" && h.customMetrics != nil && h.customMetrics.HasEventType(ce.Type) {
    var dataMap map[string]interface{}
    if err := json.Unmarshal(ce.Data, &dataMap); err == nil {
        resourceType, resourceID, tenantID = h.customMetrics.ClassifyEvent(ce.Type, dataMap)
    }
}
```

### Modified: `cmd/consumer/main.go`

Load custom metrics config after `config.Load()`, before handler creation:
```go
var cmRegistry *custommetrics.Registry
if cfg.CustomMetricsConfigPath != "" {
    cmRegistry, err = custommetrics.LoadFromFile(cfg.CustomMetricsConfigPath, logger)
    // fail startup on error
}
```
Pass `cmRegistry` to `NewHandler`.

### Modified: `internal/ingest/handler_test.go`

- Update `NewHandler` call in `TestMain` (add nil for registry).
- Add `TestIngestCustomMetricEvent` — create temp config file, load registry,
  send a CloudEvent with custom type, verify metering entries in DB.
- Add `TestIngestCustomMetricEvent_MissingField` — partial extraction, verify
  other meters still created.

### New: `deploy/custom-metrics-example.json`

Example config for docs/demo.

## What does NOT change

- **Schema** — `metering_entries` already accepts arbitrary `meter_name` values
- **Rating** — `FindRate` already matches any `(resource_type, meter_name)` pair
- **Reporting** — cost reports group by free-text fields
- **Hardcoded handlers** — VM, cluster, MaaS handlers continue working
- **No new dependencies** — stdlib only

## What is explicitly out of scope

- Hot-reload of config (restart pod to pick up changes)
- Auto-seeding rates for custom metrics (manual rate creation required)
- Inventory tracking for custom resource types (metering entries only)
- UI for managing custom metrics (API/config is sufficient for PoC)
- Billable state filtering for custom event types

## Phase 2: GoRules/Zen Integration Path

This PoC (Phase 1) deliberately keeps the two concerns separate:

```
Phase 1 (this PR)              Phase 2 (post-PoC)
─────────────────              ──────────────────
EXTRACTION                     EXTRACTION
  Custom metrics config          Same config (no change)
  "field X → meter Y"           "field X → meter Y"

RATING                         RATING
  Simple: value × price          GoRules/Zen engine
  Tiered: tier brackets          Multi-variable decisions
  Per-tenant overrides           Formula-based pricing
                                 Visual rule editor (React)
```

### What Phase 1 can't do (and GoRules would)

| Capability | Example | Phase 1 | Phase 2 (GoRules) |
|---|---|---|---|
| Cross-field decisions | "if gpu_model == A100 → rate X; if H100 → rate Y" | Needs separate resource_types | Decision table |
| Formula-based meters | "value = tokens_in × tokens_out" | One field per meter | Expression node |
| Conditional metering | "only meter if state == RUNNING" | No state filtering | Condition node |
| Multi-variable pricing | "cost = f(tokens_in, model_tier, region)" | One rate per meter | Decision graph |
| Visual rule design | Non-developer defines pricing | SQL + JSON config | JDM Editor (React UI) |

### Why the separation matters for GoRules

GoRules/Zen operates on the **rating** side, not the extraction side.
It replaces `ApplyRate(value, rate)` with a decision engine that can
evaluate complex rules. The extraction config we're building stays
the same — it feeds metering entries into whatever rating engine is
active.

The integration point is clean:

```
CloudEvent
  → [custom metrics config] → MeteringEntry
  → [Phase 1: ApplyRate]    → CostEntry      ← replace this
  → [Phase 2: GoRules/Zen]  → CostEntry      ← with this
```

### GoRules integration sketch (Phase 2)

1. **JDM rules file** replaces the `rates` table as the pricing definition.
   Rules are JSON, stored in a ConfigMap or file, versioned in git.

2. **Zen engine** (Go bindings via CGo) evaluates rules at rating sweep time.
   Input: the full metering entry fields. Output: cost amount + cost type.

3. **JDM Editor** (React app, MIT license) provides a visual UI for
   operators to define pricing rules without code. Could be embedded in
   a simple admin page or run standalone.

4. The `rates` table either becomes a seed source for JDM rules (migration
   path) or remains as a fallback for simple flat/tiered rates.

### Design decisions that keep Phase 1 forward-compatible

- **Metering entries are the interface boundary.** Both phases consume the
  same `metering_entries` rows. The extraction config never needs to change
  when the rating engine changes.
- **No rating logic in the extraction config.** The config says "extract
  these fields as meters" — it doesn't say how to price them. This means
  GoRules can be added without touching the custom metrics config format.
- **Rating engine is swappable.** `ApplyRate` is a pure function called from
  `sweep`. Replacing it with a GoRules evaluation is a localized change in
  `rating.go`, not a pipeline redesign.

### Reference

See [rating-engine-options.md](rating-engine-options.md) for the full
GoRules/Zen evaluation, CloudKitty comparison, and the two-phase
recommendation.

## Verification

1. `go build ./cmd/consumer/` — compiles
2. `go vet ./...` — clean
3. `go test ./internal/custommetrics/` — unit tests pass
4. `go test ./internal/ingest/` — existing + new integration tests pass
5. Manual test: start the service with `CUSTOM_METRICS_CONFIG` pointing to
   the example file, POST a CloudEvent with the custom type, verify metering
   entries appear in the database and get rated if a matching rate exists
