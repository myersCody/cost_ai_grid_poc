# Metering Approaches: Local Sweep vs OSAC Collector

> For discussion with Moti — comparing how we produce metering entries today
> vs how the OSAC metering collector (`osac-metering-discover-poc`) does it.

## The Two Approaches

Both produce identical output — `metering_entries` rows with the same meter
names, same values, same granularity. The difference is where the work runs.

### Approach A: Local Sweep (what we built)

```
OSAC Watch stream → Watcher → inventory tables (local PostgreSQL)
                                     ↓
                        Metering Sweep goroutine (every 60s)
                        reads local inventory, calculates duration
                                     ↓
                              metering_entries
```

The sweep goroutine queries our own `inventory_compute_instance` table for
all RUNNING VMs, calculates `duration = now - last_metered_at`, and writes
`cores × duration` to `metering_entries`.

### Approach B: OSAC Collector (what Moti's team built)

```
OSAC REST API ← Collector script polls every 60s
                 reads live data, calculates duration
                          ↓
                   CloudEvent JSON
                          ↓
                   POST to OpenMeter (currently)
                   POST to our endpoint (Phase 4)
                          ↓
                   metering_entries
```

The collector shell script calls `GET /api/fulfillment/v1/compute_instances`
every 60 seconds, filters for RUNNING, calculates `cores × 60`, and POSTs
a CloudEvent to OpenMeter (or in Phase 4, to us).

## Side-by-Side Comparison

| Aspect | Local Sweep (A) | OSAC Collector (B) |
|---|---|---|
| **Who calculates duration** | Cost Management | OSAC collector |
| **Data source for specs** | Local inventory table | OSAC REST API (live) |
| **How inventory stays fresh** | Watch stream (real-time push) | Not needed — reads live every poll |
| **Load on OSAC API** | Watch stream only (1 connection) | REST poll every 60s (N API calls) |
| **Timing accuracy** | ±60s (sweep granularity) | ±60s (same — poll granularity) |
| **Network dependency per sweep** | None — reads local DB | Requires OSAC API available |
| **Resilience to OSAC downtime** | Sweep continues with last known state | Collector blocks if API down |
| **Restart recovery** | `last_metered_at` covers the gap automatically | Must track last emission time externally |
| **Transport** | Internal (no network hop) | HTTP POST per event |
| **Implementation** | Go goroutine | Bash script + Python |
| **Output format** | Direct DB insert | CloudEvents JSON |
| **Where it runs** | Inside Cost Management process | Separate process (on OSAC side) |
| **Cross-team dependency** | None | OSAC must run and maintain the collector |

## What's the Same

- **Same math**: `cpu_core_seconds = cores × duration_seconds`
- **Same billable states**: RUNNING for VMs, READY/PROGRESSING for clusters
- **Same interval**: 60 seconds
- **Same output**: identical metering entries

## Arguments for Local Sweep (A)

1. **No cross-team dependency** — we ship independently, no coordination
   needed with OSAC team for metering to work.

2. **Less load on OSAC** — the Watch stream is a single persistent
   connection. The collector polls the REST API every 60 seconds for every
   resource type, which is N API calls per interval.

3. **Resilient to OSAC outages** — if OSAC's API goes down for 5 minutes,
   our inventory retains the last known state and the sweep continues
   producing metering entries. The collector would stop.

4. **Clean restart recovery** — `last_metered_at` is persisted in the DB.
   After a crash and restart, the first sweep covers the exact gap. The
   collector script has no built-in state recovery.

5. **One process** — metering is a goroutine inside the same binary, not
   a separate script to deploy and monitor.

## Arguments for OSAC Collector (B)

1. **OSAC controls the clock** — OSAC knows exactly when resources changed
   state. If a VM's cores are upgraded mid-interval, the collector sees the
   new spec immediately. Our sweep uses the spec stored at last Watch event.

2. **Single source of truth** — metering quantities come directly from OSAC's
   authoritative data, not from a local copy that could drift.

3. **Decoupled** — if Cost Management is down, the collector can buffer
   events (e.g., in Kafka) for later consumption. Our sweep produces
   nothing if the consumer isn't running.

4. **Reusable** — the collector can emit events to multiple consumers
   (Cost Management, OpenMeter, audit system) without each consumer
   reimplementing the sweep logic.

5. **Standard format** — CloudEvents are an interoperability standard.
   The local sweep writes directly to PostgreSQL, which only we can consume.

## Arguments for Both Together (Hybrid)

There's a case for running both:

- **Watch stream + Reconciler** for inventory (what we do today)
- **OSAC Collector** for metering events (replacing our sweep)
- **Local sweep as fallback** if collector events stop arriving

This gives the best of both: OSAC's authoritative metering data when
available, with automatic fallback to local calculation if delivery fails.
The `last_metered_at` bookmark makes this safe — if collector events arrive,
they update `last_metered_at`; if they stop, the sweep picks up from there.

## What Needs to Happen for Approach B

If we adopt the OSAC collector as the metering source:

1. **Collector points to us** — change the target from OpenMeter to our
   `POST /api/v1/events` endpoint (or a Kafka topic we consume)

2. **CaaS events** — `collect-caas.sh` already produces cluster CloudEvents.
   We need a handler for `osac.cluster.lifecycle` in the ingest endpoint.

3. **MaaS events** — no collector exists. OSAC needs to define the MaaS
   CloudEvent schema and build a collector (or Cost Management ingests
   from RHOAI directly).

4. **Transport agreement** — HTTP push? Kafka? What happens when Cost
   Management is temporarily down?

5. **Interval agreement** — 10s, 30s, or 60s? Smaller intervals = more
   events = higher DB volume.

## Recommendation

**For the PoC (now):** Keep the local sweep. It works, it's tested, it has
no cross-team dependencies, and the deadline is July 31.

**For production (Phase 4):** Adopt the hybrid approach — consume OSAC
collector events as the primary metering source, keep the local sweep as a
fallback. This requires the OSAC team to connect the collector to us, which
is a separate work item.

**Key point for the discussion with Moti:** We're not choosing one or the
other permanently. The metering_entries table is the interface boundary.
Today our sweep writes to it. Tomorrow the collector can write to it. Both
can coexist during the transition. The only question is timing and who does
the work to connect them.
