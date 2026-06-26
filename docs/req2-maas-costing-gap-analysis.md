# Requirement 2: MaaS Costing — Gap Analysis

> **Requirement:** Consumption-based rating for Model-as-a-Service. OSAC emits
> CloudEvents with token counts (in, out, inference) and request counts. Cost
> must compute pricing within 60 seconds of receiving data.
>
> **Source:** [requirements brief, section 2](https://github.com/martinpovolny/cost_ai_grid_poc/blob/main/docs/requirements/ai_grid_poc_requirements_brief.md#2-maas-costing--must-have)

## Key Difference from Req #1

MaaS is **consumption-based**, not capacity-based. You pay for what you use
(tokens processed, requests made), not for what's provisioned. This is
fundamentally different from CaaS/VMaaS where a running VM costs the same
regardless of utilization.

| Aspect | CaaS/VMaaS (req #1) | MaaS (req #2) |
|---|---|---|
| Billing model | Capacity-based | Consumption-based |
| What's metered | Provisioned resources × time | Actual usage (tokens, requests) |
| Example meter | vm_cpu_core_seconds = 4 cores × 60s | maas_tokens_in = 15,000 tokens |
| Rate structure | $/core-hour or $/VM-month | $/million tokens |
| Data source | Resource lifecycle (exists/doesn't) | Usage metrics (volume processed) |

## What Exists in OSAC Today

### OSAC Fulfillment Service

**No Model/MaaS entities exist.** Searched the fulfillment-service proto
definitions:

- No `model` proto type
- No `Model` in the Watch stream event `oneof payload`
- No `/api/fulfillment/v1/models` endpoint
- No MaaS-related gRPC services

The fulfillment-service currently manages: clusters, compute instances,
bare metal instances, networking, identity, and instance types. Models
are not yet part of this service.

### Metering Collector PoC

The [osac-metering-discover-poc](https://github.com/masayag/osac-metering-discover-poc)
repository has collectors for CaaS and VMaaS only. No MaaS collector exists.

### Event Type Definition (our repo)

The `docs/event-types.md` on the main branch defines a **proposed** MaaS
CloudEvent schema, explicitly marked as:

> **Status: Not yet defined by OSAC.** The schema below is a proposal based
> on expected RHOAI (OpenShift AI) metrics. To be confirmed.

Proposed event type: `osac.model.lifecycle`

Proposed fields:
| Field | Type | Description |
|---|---|---|
| `tenant_id` | string | Tenant identifier |
| `model_id` | string | Unique model deployment UUID |
| `model_name` | string | Model identifier (e.g. `llama-3-8b`) |
| `template` | string | MaaS template ID |
| `state` | string | Model deployment state |
| `tokens_in` | int | Input tokens processed in this interval |
| `tokens_out` | int | Output tokens generated in this interval |
| `inference_tokens` | int | Total inference tokens (in + out) |
| `requests` | int | Number of inference requests |
| `duration_seconds` | int | Elapsed seconds since last event |

Proposed meters:
| Meter Name | Unit |
|---|---|
| `maas_tokens_in` | tokens |
| `maas_tokens_out` | tokens |
| `maas_inference_tokens` | tokens |
| `maas_requests` | requests |

## Open Questions (Blockers from OSAC Side)

These questions from the requirements brief are **still unresolved** — they
require OSAC team input before we can finalize the implementation:

### 1. Who collects MaaS metrics — Cost or OSAC?

RHOAI (OpenShift AI) is the system that serves model inference. Metrics
like token counts and request counts originate there. Two options:

- **OSAC collects** from RHOAI and forwards to Cost via events (preferred —
  keeps Cost as a pure consumer)
- **Cost collects** directly from RHOAI (makes Cost coupled to RHOAI internals)

**Impact on us:** If OSAC collects, we just add a new event handler. If Cost
must collect, we need a separate RHOAI integration — different API, different
auth, different data format.

### 2. What fields will MaaS CloudEvents contain?

The proposed schema is reasonable but unconfirmed. Key unknowns:
- Are `tokens_in` and `tokens_out` per-interval increments or cumulative?
  (Increments are easier for metering; cumulative requires delta calculation)
- Is `inference_tokens` always `tokens_in + tokens_out`, or can it differ?
- Is `model_name` a stable identifier we can use for rate lookups?
- What states does a model deployment have? (`MODEL_STATE_RUNNING`, etc.)

### 3. Will Model be an OSAC entity?

If OSAC adds a Model entity to the fulfillment-service (like ComputeInstance),
it would appear in the Watch stream and we'd handle it the same way. If
models are NOT managed by OSAC, we need a different integration path.

## What We Can Implement Now

Despite the unknowns, the **metering pipeline is generic enough** that we can
add MaaS support based on the proposed schema. The pipeline doesn't care
whether the event comes from a Watch stream or a future CloudEvents source —
it just needs: resource_type, resource_id, tenant_id, and meter values.

### What we'll build:

1. **Model inventory table** (`inventory_model`) — track model deployments
   with state, model_name, template, tenant

2. **MaaS meter definitions** — `maas_tokens_in`, `maas_tokens_out`,
   `maas_inference_tokens`, `maas_requests` in the metering pipeline

3. **Simulated MaaS event ingestion** — since OSAC doesn't emit model events
   yet, we'll create a test endpoint or script that generates mock MaaS
   events matching the proposed schema, feeding them through the same
   metering pipeline

4. **Consumption-based metering** — unlike VMs where we calculate
   `cores × duration`, MaaS meters are direct sums of event values
   (`tokens_in` from event → `maas_tokens_in` meter entry)

### What we explicitly defer:

- **Rate calculation** — computing cost from metering entries (req #6, not #2).
  The rate structure ($/million tokens) is defined but rate lookup and tiered
  pricing are a separate requirement.

- **RHOAI integration** — we won't connect to a real RHOAI instance.
  We'll use the proposed event schema with mock data.

- **60-second SLA** — already met by the existing pipeline architecture.
  Events are processed synchronously as they arrive.

## Implementation Progress

### Completed

1. **Model inventory** — `inventory_model` table tracking model deployments
   with model_name, tenant, project, template, state.

2. **MaaS metering pipeline** — consumption-based metering with 4 meters:
   `maas_tokens_in`, `maas_tokens_out`, `maas_inference_tokens`,
   `maas_requests`. Event-driven (no periodic sweep needed).

3. **Ingest endpoint** — HTTP POST `/api/v1/events` accepts MaaS CloudEvents
   and processes them through the full pipeline (raw_events → inventory →
   metering). Enabled via `INGEST_LISTEN_ADDR` env var.

4. **MaaS simulator** — Go binary (`maas-simulator`) generates randomized
   MaaS CloudEvents across 4 models (llama-3-8b, llama-3-70b, mistral-7b,
   granite-34b) and 3 tenants at configurable rate.

5. **Throughput verified:**

   | Events | Workers | Throughput |
   |---|---|---|
   | 1,000 | 8 | 1,164 events/s |
   | 5,000 | 16 | 1,632 events/s |
   | 10,000 | 16 | 1,707 events/s |

   A sovereign cloud with 100 models × 10 req/s = ~17 metering events/second
   at 60-second collection intervals. Pipeline handles 100x that.

### Remaining Gaps

| Capability | Status | Notes |
|---|---|---|
| MaaS CloudEvents schema | **Proposed only** | Not confirmed by OSAC — see open questions above |
| OSAC Model entity | **Does not exist** | No proto, no API, no Watch stream events |
| Rate structure | **Defined, not implemented** | $/million tokens — rate engine is req #6 |
| RHOAI metric collection | **Unresolved** | Who collects: OSAC or Cost? |

## Coverage vs Gaps

| Capability | Required | Status | Notes |
|---|---|---|---|
| Model inventory tracking | Yes | **Done** | `inventory_model` table |
| MaaS event ingestion | Yes | **Done (mock)** | Ingest endpoint + simulator; blocked on real OSAC events |
| Token/request metering | Yes | **Done** | 4 meters, consumption-based |
| Rate structure definition | Yes | **Documented** | $/million tokens for in/out/inference/requests |
| Cost computation within 60s | Yes | **Met** | <1ms per event at 1700 events/s |
| MaaS CloudEvents schema | Yes | **Proposed only** | Not confirmed by OSAC |
| Throughput testing | Yes | **Done** | 1,700 events/s sustained, 100x realistic load |

## Processing Pipeline for MaaS

```
MaaS event received (mock or future OSAC event)
  → INSERT into raw_events
  → upsert inventory_model (state, model_name, tenant)
  → extract meters:
      maas_tokens_in      = event.tokens_in
      maas_tokens_out     = event.tokens_out
      maas_inference_tokens = event.inference_tokens
      maas_requests       = event.requests
  → INSERT into metering_entries (one row per meter)
```

Note: no duration calculation needed. The event carries absolute consumption
values for the interval. This is simpler than the VM metering sweep.

## Differences from VM Metering

| Aspect | VM metering (req #1) | MaaS metering (req #2) |
|---|---|---|
| Duration tracking | Yes — `last_metered_at` | No — events carry consumption directly |
| Periodic sweep | Yes — every 60s | No — meter on event arrival |
| Billable state filter | Yes — only RUNNING | Yes — only RUNNING models |
| Zero-usage events | Possible (VM idle) | Unlikely (no tokens = no event) |
| Final metering on DELETE | Yes — covers gap to deletion | No — consumption stops when model stops |
| Meter values | Derived (cores × seconds) | Direct from event (token counts) |

## Implementation Plan

1. Add `inventory_model` table and `ModelRecord` type
2. Add MaaS billable state (`MODEL_STATE_RUNNING`)
3. Add mock MaaS event ingestion (script or API endpoint)
4. Add MaaS meter extraction in the metering pipeline
5. Add tests
6. Document the proposed rate structure

## Rate Structure (from requirements)

Pricing per million units:

| Meter | Rate Basis | Example |
|---|---|---|
| `maas_tokens_in` | $/million input tokens | $0.50/M tokens |
| `maas_tokens_out` | $/million output tokens | $1.50/M tokens |
| `maas_inference_tokens` | $/million inference tokens | $1.00/M tokens |
| `maas_requests` | $/million requests | $5.00/M requests |

Rates would vary by model (GPT-4 class vs. small models). This is a rate
engine concern (req #6), but the metering pipeline must produce entries
that can be looked up by `meter_name` + `model_name`.
