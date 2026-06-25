# ADR-001: Metering Sweep Interval

## Status
Accepted

## Context

The OSAC fulfillment-service Watch stream emits events on state transitions
(CREATED, UPDATED, DELETED) — not periodic heartbeats. However, capacity-based
billing requires knowing how long each resource was provisioned, which means
we need to periodically record "this resource was running for N seconds."

The OSAC CloudEvents spec (event-types.md) defines a metering collector that
emits events every ~60 seconds with a `duration_seconds` field. This collector
is still early-stage and not available for integration yet.

We need a mechanism to produce metering entries (e.g., `vm_uptime_seconds`,
`vm_cpu_core_seconds`) for all billable resources on a regular cadence.

## Decision

Run a **metering sweep every 60 seconds** that:

1. Queries all resources in billable states (RUNNING for VMs,
   READY/PROGRESSING for clusters)
2. Calculates duration since last metering (`now - last_metered_at`, or
   `now - created_at` for first metering)
3. Produces metering entries: `vm_uptime_seconds`, `vm_cpu_core_seconds`,
   `vm_memory_gib_seconds` (one row per meter per resource per sweep)
4. Updates `last_metered_at` on the inventory record

Additionally, on DELETE events, produce a **final metering entry** covering
the time from `last_metered_at` to the deletion timestamp, so no usage is
lost between the last sweep and deletion.

## Why 60 seconds

- **Matches the OSAC metering collector interval** — when the collector is
  ready and we switch to consuming its CloudEvents, the metering entries
  will have the same granularity
- **Meets the 60-second processing SLA** from the requirements ("Cost must
  process within 60 seconds of receipt")
- **Sufficient for quota enforcement** — checking quota consumption at
  60-second granularity is adequate for capacity-based billing
- **Low overhead** — one SELECT + batch INSERT per sweep for all billable
  resources, no per-second polling

## Alternatives Considered

- **Derive from inventory timestamps at query time** — our original
  summarizer approach. Works for daily reports but can't support real-time
  quota checks or per-event metering entries.
- **5-second sweep** — higher resolution but unnecessary overhead for
  capacity billing. The smallest billing unit (VM-minute or cluster-minute)
  doesn't need sub-minute precision.
- **Event-driven only (no sweep)** — only produce metering on Watch stream
  events. Misses the time dimension: if a VM is RUNNING and no events
  arrive, no metering entries are produced.

## Consequences

- Metering entries accumulate at ~1 row per meter per resource per minute.
  For 100 VMs with 3 meters each: ~432,000 rows/day. Manageable with
  appropriate indexing and periodic aggregation.
- When the OSAC metering collector is available, we can switch the metering
  source from our sweep to the collector's CloudEvents. The `metering_entries`
  table stays the same — only the producer changes.
- The `last_metered_at` column is the reconciliation point. On restart,
  the first sweep covers exactly the gap since shutdown.
