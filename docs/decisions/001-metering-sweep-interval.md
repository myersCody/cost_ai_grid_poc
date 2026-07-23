# ADR-001: Metering Sweep Interval

## Status
Accepted — **updated Jul 2026** with end-to-end latency analysis and
interval reduction (60s→45s metering, 30s→20s rating).

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

Run a **metering sweep every 45 seconds** that:

1. Queries all resources in billable states (RUNNING for VMs,
   READY/PROGRESSING for clusters)
2. Calculates duration since last metering (`now - last_metered_at`, or
   `now - created_at` for first metering)
3. Produces metering entries: `vm_uptime_seconds`, `vm_cpu_core_seconds`,
   `vm_memory_gib_seconds` (one row per meter per resource per sweep)
4. Updates `last_metered_at` on the inventory record

The **rating sweep runs every 20 seconds**, picking up unrated metering
entries and producing cost entries.

Additionally, on DELETE events, produce a **final metering entry** covering
the time from `last_metered_at` to the deletion timestamp, so no usage is
lost between the last sweep and deletion.

## End-to-End Latency Analysis

The SLA from the requirements spec:

- OSAC emits within **30s** of event (contractual ceiling)
- Cost must process within **60s** of receipt
- End-to-end SLA: **90s** from event to cost entry

### Two metering paths

| Path | Examples | Metering | Latency |
|---|---|---|---|
| **Event-driven** | MaaS CloudEvents, VM/BM DELETE | Inline on event arrival | 0s |
| **Sweep-driven** | VM/Cluster/BM CREATED/UPDATED (capacity) | Next metering sweep tick | ≤interval |

Event-driven metering is already used for MaaS (`MeterMaaSEvent`) and
final metering on delete (`MeterComputeInstanceFinal`,
`MeterBareMetalInstanceFinal`). These paths have zero metering delay.

Sweep-driven metering is used for ongoing capacity billing — the sweep
records "this resource was running for N more seconds." This is where
the interval matters for latency.

### Worst-case timing (sweep-driven path)

With Watch stream delivery, OSAC emit latency is effectively 0 — the
event fires on database commit. The 30s SLA is a contractual ceiling
for edge cases (batch collectors, eventual consistency), not the
typical path. So:

```
T=0   Event occurs → OSAC emits via Watch (~instant)
T=0   Watch delivers → inventory updated
T=M   Metering sweep fires (worst case: just missed → wait full interval)
T=M+R Rating sweep fires (worst case: just missed → wait full interval)
Total worst case = M + R
```

Where M = metering interval, R = rating interval.

### Interval comparison

| Metering | Rating | Worst case | Average case | Margin vs 90s |
|---|---|---|---|---|
| 60s | 30s | 90s | 45s | 0s (at the limit) |
| 50s | 25s | 75s | 37s | 15s |
| **45s** | **20s** | **65s** | **32s** | **25s** |
| 30s | 15s | 45s | 22s | 45s |

Average case = M/2 + R/2 (uniform distribution of event arrival
within the sweep cycle).

### With OSAC emit latency (contractual worst case)

If OSAC genuinely takes the full 30s to emit (unlikely with Watch
stream, but the contractual bound):

| Metering | Rating | Worst case (incl 30s emit) | Margin |
|---|---|---|---|
| 60s | 30s | 120s | -30s (busts SLA) |
| 50s | 25s | 105s | -15s (busts SLA) |
| **45s** | **20s** | **95s** | **-5s (tight but 30s emit is unrealistic with Watch)** |
| 30s | 15s | 75s | 15s |

With Watch stream, OSAC emit is ~0s, so the realistic worst case with
45s/20s is **65s** — well within the 90s SLA with 25s margin.

### Chosen intervals: 45s metering, 20s rating

- **65s realistic worst case** (Watch stream delivery)
- **32s average case**
- **25s margin** against the 90s SLA
- Still low overhead: one SELECT + batch INSERT per sweep
- When OSAC's metering collector ships (60s heartbeat events), we can
  switch to event-driven capacity metering and the sweep becomes
  catch-up only

## Why not event-driven for CREATED/UPDATED?

An alternative is to create metering entries inline when the Watch
event arrives (like we already do for MaaS and DELETE). This would
eliminate the sweep delay entirely for the first metering entry.

We chose not to do this for capacity resources because:

- The Watch event says "resource exists" — it doesn't carry
  `duration_seconds`. The first metering entry would have value 0.
- Ongoing metering still needs the sweep (resource stays RUNNING, no
  new events arrive, but time passes and usage accrues).
- The sweep is the single source of duration calculation — splitting
  it between event-driven (first entry) and sweep (subsequent entries)
  adds complexity for marginal latency gain.

If the 90s SLA tightens, event-driven first-entry metering is the
next step. The infrastructure exists (`MeterMaaSEvent` pattern).

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
- **60s metering + 30s rating (original)** — meets the SLA exactly at
  the worst-case boundary with Watch stream, but leaves zero margin.
  Not acceptable when demonstrating SLA compliance.

## Consequences

- Metering entries accumulate at ~1 row per meter per resource per 45s.
  For 100 VMs with 3 meters each: ~576,000 rows/day (vs 432,000 at 60s).
  33% increase — manageable with appropriate indexing and periodic
  aggregation.
- Rating sweep at 20s means cost entries appear faster after metering,
  improving responsiveness of quota status API and dashboard.
- When the OSAC metering collector is available, we can switch the metering
  source from our sweep to the collector's CloudEvents. The `metering_entries`
  table stays the same — only the producer changes.
- The `last_metered_at` column is the reconciliation point. On restart,
  the first sweep covers exactly the gap since shutdown.
