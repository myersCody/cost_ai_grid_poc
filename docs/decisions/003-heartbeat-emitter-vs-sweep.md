# ADR-003: Heartbeat Events vs. Local Sweep — What They Are, How the PoC Works, and What OSAC Must Deliver

## Status
Accepted (PoC) — HTTP ingest path **implemented**; Phase 4 now requires OSAC URL redirect only

---

## Background: What Are "Heartbeat Events"?

The requirements (REQ-1b) refer to "heartbeat events" from OSAC. This caused confusion — it sounds like a distinct event type with its own schema. It is not.

**A heartbeat event is the same OSAC lifecycle CloudEvent type (`osac.cluster.lifecycle`, `osac.compute_instance.lifecycle`, etc.) emitted periodically on a timer rather than only on a state transition.**

The distinction is the *trigger*, not the schema:

| Pattern | Trigger | Key additional fields | Purpose |
|---|---|---|---|
| **State transition** | Resource created, updated, or deleted | None beyond base schema | Inventory sync — keep Cost Management's record of what exists |
| **Heartbeat** | Timer in OSAC's metering collector (every N seconds) | `duration_seconds` + pre-calculated quantities (`cpu_core_seconds`, `worker_node_seconds`, etc.) | Metering — produce time-based cost entries for billable resources |

This was confirmed in the Jun 23, 2026 meeting (~00:21:32). When asked whether heartbeat events carry only "I'm alive" or also capacity details, OSAC confirmed: *"the cluster order details with the currently configured hardware."* The reference implementation is the [osac-metering-discover-poc](https://github.com/masayag/osac-metering-discover-poc) collector scripts.

---

## Two Separate OSAC Data Sources

To understand the heartbeat vs. sweep distinction, it helps to know that OSAC exposes two completely separate interfaces — with different formats and different purposes:

### 1. Fulfillment Service Watch Stream (`/api/private/v1/events/watch`)

This is what the PoC consumes today. It is a **streaming HTTP endpoint** that emits a newline-delimited JSON event every time a resource changes state. The event types are:

- `EVENT_TYPE_OBJECT_CREATED`
- `EVENT_TYPE_OBJECT_UPDATED`
- `EVENT_TYPE_OBJECT_DELETED`

These events carry the resource's current spec and state (e.g. a `Cluster` with its `node_sets`, `template`, and `state`). **They are not CloudEvents.** They do not carry `duration_seconds`, `cpu_core_seconds`, or any pre-calculated metering quantity. Their only job is inventory sync — telling Cost Management what exists and what it looks like right now.

The PoC's `inventory-watcher` consumes this stream, stores the resource in the inventory tables, and records the event source as `"osac.fulfillment-service"`.

### 2. OSAC Metering Collector (`osac-metering-discover-poc`)

This is a **separate set of shell scripts** that poll the fulfillment service REST API, calculate elapsed time and metering quantities, and emit properly-formatted **CloudEvents** (`osac.cluster.lifecycle`, `osac.compute_instance.lifecycle`) with `duration_seconds`, `worker_node_seconds`, `cpu_core_seconds`, etc. already filled in.

These scripts exist and work, but they currently send their output to **OpenMeter**, not to Cost Management. They are not connected to the PoC and are not required for the demo.

The CloudEvent schemas in [event-types.md](../poc_architecture/event-types.md) describe what this collector emits — they are the **target format** for Phase 4, not what the PoC receives today via the Watch stream.

**Update (Jun 27, 2026):** The PoC's `POST /api/v1/events` HTTP ingest endpoint now accepts all three CloudEvent types in the exact format the OSAC metering collector emits. Pre-calculated values (`duration_seconds`, `cpu_core_seconds`, `memory_gib_seconds`, `worker_node_seconds`) are written directly to `metering_entries` without local recalculation. `last_metered_at` is updated on receipt to prevent the local sweep from double-counting the same interval. Switching the collector's target from OpenMeter to Cost Management requires only a URL change — no format translation or schema changes needed on either side.

### Summary

| | Fulfillment Service Watch Stream | POC Metering Collector |
|---|---|---|
| Format | Fulfillment service protobuf/JSON (not CloudEvents) | CloudEvents 1.0 structured JSON |
| Trigger | Resource state change | Timer (~60s) |
| Contains metering quantities? | No | Yes (`duration_seconds`, `cpu_core_seconds`, etc.) |
| Connected to Cost Management PoC? | **Yes** | **Partially** — HTTP ingest endpoint accepts the collector's format; collector still sends to OpenMeter today |
| Purpose | Inventory sync | Metering / capacity billing |

The local sweep described below exists precisely because the Watch stream alone cannot drive billing — it only fires on state changes — and the POC metering collector is not yet connected.

---

## The Problem Heartbeat Events Solve

State transitions alone cannot drive capacity billing. A VM that starts `RUNNING` and stays running emits no further Watch stream events. Without a periodic signal, Cost Management has no way to know the VM is still running and accumulating cost.

Heartbeat events solve this by having OSAC emit a periodic event for every active resource, carrying the current hardware spec and a pre-calculated `duration_seconds` (time elapsed since the last emission). Cost Management receives the event, writes metering entries directly from the payload, and moves on.

---

## Decision: Use a Local Sweep for the PoC

The PoC does not require OSAC to deliver the heartbeat collector. Instead, Cost Management implements a **local 60-second metering sweep** that replicates what heartbeat events would provide.

### How the sweep works

See [ADR-001: Metering Sweep Interval](001-metering-sweep-interval.md) for the full mechanics, interval rationale, and alternatives considered. In summary: every 60 seconds the `inventory-watcher` queries all billable resources, calculates `duration_seconds = now - last_metered_at`, derives metering quantities from the stored spec, and writes one `metering_entries` row per meter per resource. A final entry is written on DELETE to cover the gap since the last sweep.

### What the sweep produces vs. what heartbeat events would produce

The output is identical — same `metering_entries` rows, same meter names, same values. The only difference is who computed the duration:

| | Local sweep (PoC) | Heartbeat events (target) |
|---|---|---|
| Duration calculated by | Cost Management (`now - last_metered_at`) | OSAC collector (`ce_time - prev_emission_time`) |
| Timing accuracy | ±60s (sweep granularity) | Higher — OSAC controls the clock |
| Transport | Internal (no network hop) | HTTP or Kafka push from OSAC |
| OSAC work required | None | Collector must be connected to Cost Management |
| `metering_entries` schema | Unchanged | Unchanged |

---

## Tradeoffs: Local Sweep vs. Heartbeat Emitter

### Local sweep

**Pros**
- No cross-team dependency — Cost Management controls the clock and can ship independently
- Simple to debug: all metering logic lives in one place, no external delivery to reason about
- Resilient to OSAC availability — sweep runs even if OSAC is down or slow to respond
- Clean restart recovery: `last_metered_at` automatically covers any gap during downtime

**Cons**
- Cost Management must own and maintain sweep infrastructure long-term
- ±60s timing precision — not a problem for capacity billing, but not exact
- If a resource's hardware config changes mid-interval, the sweep uses the spec stored at sweep time, not the spec at the moment of change

### Heartbeat emitter

**Pros**
- Higher timing accuracy — OSAC controls the emission clock and knows exactly when state changed
- OSAC can pre-calculate quantities (`cpu_core_seconds`, etc.), reducing computation on the Cost Management side
- Better alignment with OSAC's authoritative view of resource state — hardware config changes are reflected immediately in the event payload
- Scales naturally: OSAC pushes only active resources; Cost Management has no polling overhead

**Cons**
- Cross-team dependency — OSAC must implement, connect, and maintain the collector
- Delivery reliability risk: if OSAC misses an emission or delivers late, metering entries have gaps; Cost Management needs a reconciliation strategy
- Transport and interval are still open questions (R-5, R-6) — the handoff carries real coordination cost
- MaaS and BMaaS schemas do not yet exist; those resource types cannot use heartbeat events until OSAC defines and commits to them

---

## Why Not Wait for the Heartbeat Collector?

1. **The collector is not yet connected to Cost Management.** The `osac-metering-discover-poc` scripts exist and produce the right events, but delivery to Cost Management over HTTP or Kafka is not yet set up (see R-5, R-6 in [event-types.md](../poc_architecture/event-types.md)).
2. **The PoC can fully demonstrate metering and cost calculation without it.** The sweep is not a workaround — it is a deliberate architectural stand-in that preserves the option to swap in heartbeat events later.
3. **The PoC deadline (July 31, 2026) is tight.** Waiting for OSAC to wire up the collector would add a cross-team dependency to the critical path.

---

## What OSAC Must Deliver for Production (Phase 4)

Three things are required before the sweep can be retired and replaced by heartbeat events:

### 1. Redirect the metering collector to Cost Management

~~The OSAC metering collector must be configured to emit its CloudEvents to a Cost Management HTTP endpoint.~~

**This is now ready on the Cost Management side.** The `POST /api/v1/events` endpoint accepts `osac.cluster.lifecycle`, `osac.compute_instance.lifecycle`, and `osac.model.lifecycle` in the exact format the collector already emits. Pre-calculated values are ingested directly; `last_metered_at` is updated to prevent double-counting with the local sweep.

**Required OSAC action:** change the collector's target URL from the OpenMeter endpoint to the Cost Management ingest endpoint. No schema changes, no format translation.

### 2. Agree on transport and interval

| Open question | Options | Decision needed by |
|---|---|---|
| Transport | **HTTP push to `POST /api/v1/events`** (endpoint exists and is verified) vs. Kafka topic | OSAC + Cost (see R-6) — HTTP path is now the path of least resistance |
| Interval | Requirements say 10–30s; existing collector uses 60s | OSAC + Cost (see R-5) |

### 3. Deliver MaaS and BMaaS schemas (separate from the sweep)

The ingest endpoint already handles `osac.model.lifecycle` (MaaS). BMaaS heartbeat events require OSAC to define and commit to a CloudEvent schema that does not yet exist (see R-3, R-4 in [event-types.md](../poc_architecture/event-types.md)).

---

## What Cost Management Has Done (Phase 4 — ingestion side complete)

The `POST /api/v1/events` ingest endpoint, implemented in `inventory-watcher/internal/api/handler.go`, already performs all five steps:

1. ✅ Receives the event via HTTP (`POST /api/v1/events`, CloudEvents 1.0 JSON)
2. ✅ Deduplicates on `ce_id` (stored in `raw_events`)
3. ✅ Extracts `duration_seconds` and pre-calculated metering quantities directly from the event payload
4. ✅ Writes `metering_entries` rows — same schema, same meter names as the local sweep
5. ✅ Updates `last_metered_at` on the inventory record (prevents sweep double-counting)

Inventory is auto-created (or upserted) from the event payload if no prior record exists — no pre-registration required.

**The sweep is disabled per-resource** once `last_metered_at` is kept current by incoming heartbeat events. In practice, disabling the sweep globally should wait until the collector is delivering reliably and all resources are covered — a gradual transition is possible.

---

## Consequences

- The PoC is fully unblocked — no dependency on OSAC heartbeat delivery for the demo.
- **The Phase 4 handoff scope is now minimal on the Cost Management side:** the ingest endpoint exists, accepts the collector's format, and is verified against all three event types. OSAC's action is a URL redirect in the collector configuration.
- The transition is low-risk — both producer patterns (sweep and HTTP ingest) write to the same `metering_entries` table with the same schema. The two paths can run in parallel during a validation period: events received via HTTP set `last_metered_at`, causing the sweep to skip those resources automatically (no double-counting).
- Timing accuracy improves in production: the sweep has ±60s precision; the collector knows exactly when state changed.
- MaaS events (`osac.model.lifecycle`) are already handled end-to-end via HTTP ingest. BMaaS remains blocked on OSAC defining the CloudEvent schema.

---

## References

- [event-types.md §Overview](../poc_architecture/event-types.md) — full CloudEvent schemas and dual emission pattern explanation
- [ADR-001: Metering sweep interval](001-metering-sweep-interval.md) — why 60 seconds
- [ADR-002: Arguments against Kafka](002-arguments-against-kafka.md) — transport choice
- [metering-spec-draft.md §4](../poc_architecture/metering/metering-spec-draft.md) — PoC vs. target implementation detail
- [osac-metering-discover-poc](https://github.com/masayag/osac-metering-discover-poc) — OSAC's existing collector reference implementation
