# Roadmap: PoC → Production

## Where We Are

The inventory-watcher has a working end-to-end pipeline:

```
OSAC Watch stream → inventory → metering (60s sweep) → rating (30s) → cost entries
MaaS ingest endpoint → inventory_model → metering → rating → cost entries
Quota status API → SUM(metering) vs limits → threshold checks
```

15 of 18 requirements done, 2 partial, 1 TBD — see [implementation-status.md](implementation-status.md) for current tracking.

## Next Steps (PoC — by July 31)

### 1. OpenMeter-compatible ingest endpoint (HIGH VALUE)

**Why:** The OSAC metering collector (`osac-metering-discover-poc`) already
produces CloudEvents and POSTs them to OpenMeter's `POST /api/v1/events`.
If we accept the same format, the collector points at us with a URL change —
zero work on OSAC side.

**What:**
- Accept `osac.compute_instance.lifecycle` and `osac.cluster.lifecycle`
  CloudEvents at `POST /api/v1/events` (same path OpenMeter uses)
- Extract `duration_seconds`, `cpu_core_seconds`, `memory_gib_seconds`
  from the payload — no calculation needed, values are pre-computed
- Write to `raw_events` + `metering_entries` (same pipeline as MaaS)
- Auto-upsert inventory from the event payload

**Result:** The local sweep becomes a fallback. When collector events arrive,
metering entries come pre-calculated from OSAC. Both can coexist safely via
`last_metered_at`.

**Effort:** Small — we already handle MaaS CloudEvents. Add two more event
type cases to the same handler.

### 2. REQ-10 — Threshold notifications (HIGH)

**Why:** REQ-9 (quota status API) is done. REQ-10 is the push side — notify
OSAC when thresholds are crossed. Together they complete the quota story.

**What:**
- On each rating sweep, check if any tenant crossed a threshold since last check
- `alerts` table to track fired alerts (avoid re-firing)
- Webhook POST to OSAC with tenant_id, meter, threshold, consumption
- Or: just set a flag that the quota API returns (pull model for PoC)

**Effort:** Medium. Pull model (add `alerts` to quota API response) is small.
Push model (webhook) needs transport agreement with OSAC.

### 3. REQ-8 — Bare metal costing (HIGH)

**Why:** OSAC has a BareMetalInstance proto. Same pattern as VM metering.

**Status update (Jun 29):** Previously blocked — BareMetalInstance is not
in the public Watch stream oneOf but IS available via the public REST List
API. Since our reconciler already polls all entity types on a timer, we can
add BareMetalInstance to the reconciler without switching to the private
stream. The Watch stream won't deliver BM events, but the periodic List
sweep covers that gap — same as we do for InstanceTypes and Projects today.

**What:**
- Add `BareMetalInstance` to the Watch stream event dispatcher
- `inventory_bare_metal_instance` table
- Meters: `bm_uptime_seconds`, `bm_cpu_core_seconds`, etc.
- Default rates

**Effort:** Small — copy the VM pattern.

### 4. Balance Check Endpoint — IPP Compatibility (HIGH VALUE)

**Why:** The OSAC Inference Platform Plugin (IPP) calls a balance check on
every inference request to gate access. If we implement this, the IPP can
use us directly as the metering backend — replacing OpenMeter.

**What:**
- `GET /api/v1/customers/{id}/entitlements/{key}/value?model={model}`
- Return `{has_access, balance, usage, overage}` — see [CloudEvents catalog](cloudevents-catalog.md#balance-check-api-ipp-plugin--cost-consumer)
- ~40 lines — reuses existing `MeteringSum()` + `QuotasForTenant()` logic

**Result:** Together with our existing `POST /api/v1/events` (usage report),
we implement both endpoints the IPP expects. Drop-in replacement for
OpenMeter/metering-simulator.

**Effort:** Small — reuse `handleQuotaStatus` logic with different input/output format.

See [MaaS simulators input](inputs/2026-06-30-maas-simulators-metering-endpoints.md).

### 5. llm-katan Integration Test (MEDIUM)

**Why:** Replace our simple `maas-simulator` with the OSAC team's
[llm-katan](https://github.com/yossiovadia/llm-katan) simulator for a
more realistic demo — it goes through the full OIDC/Authorino/IPP auth
pipeline with realistic inference payloads.

**What:**
- Set up llm-katan locally, point its metering URL at our ingest endpoint
- Verify events flow: llm-katan → IPP → `POST /api/v1/events` → our pipeline
- Test `POST /api/v1/check` balance gating with llm-katan

**Effort:** Medium — setup + configuration, no code changes on our side.

### 6. Update MaaS Token Meters to Match IPP Format (MEDIUM)

**Why:** The real IPP plugin sends 5 token dimensions: `prompt_tokens`,
`completion_tokens`, `cached_input_tokens`, `cache_creation_tokens`,
`reasoning_tokens`. We currently meter only `tokens_in`/`tokens_out`.

**What:**
- Accept both naming conventions in the ingest handler (backwards compat)
- Add `maas_tokens_cached` and `maas_tokens_reasoning` meters
- Accept event type `inference.tokens.used` alongside `osac.model.lifecycle`
- Handle `subject` = username (not tenant_id) — may need Keycloak mapping

**Details:** See [CloudEvents catalog](cloudevents-catalog.md#maas--inference-token-usage-ipp-plugin)
for the full format comparison.

**Effort:** Medium — handler changes + new rate definitions + identity mapping question.

### 7. REQ-3/REQ-5 — Report/export API (MEDIUM)

**Why:** Cost data exists but is only queryable via psql/scripts.

**What:**
- `GET /api/v1/reports/costs?tenant_id=X&group_by=meter_name&period=2026-06`
- Response in JSON; `Accept: text/csv` for CSV export
- Group by: tenant, resource_type, meter_name, resource_id

**Effort:** Medium — new handler, query builder, CSV serializer.

### 8. REQ-13 — Custom rate dimensions (HIGH)

**Why:** New in v1.1 spec. Allow arbitrary CloudEvent fields to become meters.

**What:** Research phase first. GoRules/Zen engine evaluated for this (see
[rating-engine-options.md](research/rating-engine-options.md)). For PoC, a
configurable mapping (JSON config: "extract field X from CloudEvent data as
meter Y") may suffice.

**Effort:** Medium-Large. Deferred to end of PoC if time permits.

## Path to Production (Phase 4)

### Step 1: Connect OSAC collector to us

Moti's team points the metering collector at our ingest endpoint. We accept
the same CloudEvents format. Collector events become the primary metering
source; our sweep becomes fallback.

**Prerequisite:** OpenMeter-compatible endpoint (item #1 above).

### Step 2: Hybrid metering

Both sources coexist during transition:
- Collector events → metering entries (primary)
- Local sweep → metering entries (fallback, catches gaps)
- `last_metered_at` prevents double-counting

When collector coverage is validated, sweep can be disabled per resource type.

### Step 3: MaaS and BMaaS events from OSAC

OSAC needs to:
- Define Model entity in fulfillment-service (proto, API, Watch stream)
- Define MaaS CloudEvent schema for the collector
- Define BMaaS CloudEvent schema

Until then, our MaaS ingest endpoint + simulator covers the gap.

### Step 4: Retire the sweep

Once all resource types have collector coverage and delivery is reliable:
- Disable the metering sweep goroutine
- Keep the code for emergency fallback
- All metering comes from OSAC events through the ingest endpoint

### Step 5: Production hardening

- Connection pooling and batching for high event rates
- Partitioned metering_entries and cost_entries by month
- Rate limiting on the ingest endpoint
- Authentication on API endpoints
- Helm chart for deployment (REQ POC-ENV)

## Architecture: PoC → Production

**PoC (now):**
```
OSAC Watch stream → Watcher → inventory
                                  ↓
                    Metering Sweep (60s) → metering_entries → cost_entries
                                                                  ↓
MaaS simulator → Ingest endpoint → metering_entries →        quota API
```

**Production (target):**
```
OSAC Watch stream → Watcher → inventory (state sync only)

OSAC Metering Collector → Ingest endpoint → metering_entries → cost_entries
                                                                    ↓
                                                              quota API
                                                                    ↓
                                                         threshold webhook → OSAC
```

The Watch stream stays for inventory sync. The metering sweep is replaced
by the collector. The ingest endpoint becomes the single entry point for
all metering data. Everything downstream (rating, quotas, reports) is
unchanged.

## Backlog — Consider for Reports

### FOCUS Format (COST-5710)

[COST-5710](https://redhat.atlassian.net/browse/COST-5710) — Support the
[FOCUS spec](https://focus.finops.org/focus-specification/v1-1/) (FinOps
Open Cost and Usage Specification). Filed by Pau, currently in Refinement.

Three aspects relevant to our report API:

- **FOCUS export** — generate FOCUS-formatted cost data from our
  `cost_entries` table. Our data model (resource_id, resource_type,
  metered_value, cost_amount, currency, period) maps well to FOCUS columns
  (ResourceId, ResourceType, BilledCost, PricingUnit, BillingPeriod).
  Consider FOCUS column naming when designing the report API response.
- **FOCUS ingest** — accept FOCUS-formatted files as a cost data source.
  Not a PoC concern but relevant for multi-cloud cost aggregation.
- **Named endpoints** — FOCUS export suggests per-integration endpoints
  rather than generic query params.

Not a PoC requirement, but when building the report/export API (REQ-3/REQ-5),
align field naming with FOCUS where practical so we don't have to rename
later.

### OSAC Projects → RBAC (from Pau)

OSAC has a "project" concept (we already sync `inventory_project` via the
reconciler). For authorization, two paths:

- **Koku approach:** Create one Insights RBAC role per OSAC project,
  synchronize inventories and permissions through the existing RBAC system.
- **New approach:** Use Keycloak directly, same as OSAC does for its own
  authz. Simpler integration but diverges from Koku's RBAC model.

The deeper question: this PoC ("cost-event-consumer" — needs a codename)
will eventually either merge into Koku or replace it. If merge, we need
Insights RBAC compatibility. If replace, Keycloak is cleaner. This decision
affects how we implement project-level access control.

For PoC: not in scope. Our report/quota APIs have no authz — any caller
can query any tenant. But the `tenant_id` and project filtering is there
in the data model, so adding authz later is a query-level concern, not a
schema change.

## Open Items

### Private vs Public gRPC Watch Stream

**Discovery (Jun 29, 2026):** The OSAC fulfillment-service has two event
protos — public and private. The **public** oneOf has 10 entities (what we
use today). The **private** oneOf has 28 entities, including all three
catalog items (`ClusterCatalogItem`, `ComputeInstanceCatalogItem`,
`BareMetalInstanceCatalogItem`), `BareMetalInstance`,
`BareMetalInstanceTemplate`, `StorageBackend`, networking entities, and a
new `EVENT_TYPE_OBJECT_SIGNALED` event type for reconciliation hints.

**Implication:** Switching to the private Watch stream would unblock REQ-8
(bare metal) and give us real-time catalog item events instead of
poll-only. The code change is small (URL + additional struct/handler
cases). The open question is **authorization** — confirm with OSAC team
that our consumer is allowed to use the private API.

**Action:** Ask OSAC team (Moti/Juan) whether cost-event-consumer can
consume the private Watch stream. If yes, switch; if no, continue with
public stream + REST List polling for catalog items.

### Kafka as CloudEvents Transport

**Assessment:** Adding a Kafka consumer is low complexity in our current
architecture. The `handleEvent` function in the watcher takes a
deserialized `osac.Event` struct — the transport is irrelevant downstream.
A Kafka consumer would deserialize CloudEvents from a topic and feed them
into the same `handleEvent`. No changes to metering, rating, inventory, or
any downstream code.

**Work estimate:**
- Add `confluent-kafka-go` or `segmentio/kafka-go` dependency
- New `kafkaconsumer` package (~100-150 lines): connect, subscribe,
  deserialize, call `handleEvent`
- Config: `KAFKA_BROKERS`, `KAFKA_TOPIC`, `KAFKA_GROUP_ID` env vars
- Wire into `main.go` errgroup alongside the existing watcher

The `metering_entries` table remains the interface boundary — it doesn't
matter whether events arrive via Watch stream, HTTP ingest, or Kafka.

## Key Insight

The `metering_entries` table is the interface boundary. It doesn't matter
who writes to it — our sweep, the OSAC collector, or a future Kafka
consumer. Everything downstream (rates, costs, quotas, reports) reads from
this table and doesn't care where the data came from. This is what makes
the migration path safe.
