# Roadmap: PoC → Production

## Where We Are

The inventory-watcher has a working end-to-end pipeline:

```
OSAC Watch stream → inventory → metering (60s sweep) → rating (30s) → cost entries
MaaS ingest endpoint → inventory_model → metering → rating → cost entries
Quota status API → SUM(metering) vs limits → threshold checks
```

9 of 16 requirements done, 4 partial, 3 not started.

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

**What:**
- Add `BareMetalInstance` to the Watch stream event dispatcher
- `inventory_bare_metal_instance` table
- Meters: `bm_uptime_seconds`, `bm_cpu_core_seconds`, etc.
- Default rates

**Effort:** Small — copy the VM pattern.

### 4. REQ-3/REQ-5 — Report/export API (MEDIUM)

**Why:** Cost data exists but is only queryable via psql/scripts.

**What:**
- `GET /api/v1/reports/costs?tenant_id=X&group_by=meter_name&period=2026-06`
- Response in JSON; `Accept: text/csv` for CSV export
- Group by: tenant, resource_type, meter_name, resource_id

**Effort:** Medium — new handler, query builder, CSV serializer.

### 5. REQ-13 — Custom rate dimensions (HIGH)

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

## Key Insight

The `metering_entries` table is the interface boundary. It doesn't matter
who writes to it — our sweep, the OSAC collector, or a future Kafka
consumer. Everything downstream (rates, costs, quotas, reports) reads from
this table and doesn't care where the data came from. This is what makes
the migration path safe.
