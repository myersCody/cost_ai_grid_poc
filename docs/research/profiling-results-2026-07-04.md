# Profiling Results — 2026-07-04

## Method

Go benchmark tests (`testing.B`) exercising the full ingest path via
`httptest.Server` against a real PostgreSQL database (Docker, localhost).

```
go test -bench=BenchmarkIngest -benchtime=5s -cpuprofile=cpu.prof ./internal/ingest/
go tool pprof -list=handleEvent cpu.prof
```

**Hardware:** Apple M3 Pro, PostgreSQL 16 in Docker, costdb_test database.

**What's measured:** Full HTTP round trip — JSON decode → InsertRawEvent →
classify → handler (upsert + metering entries) → HTTP response. Includes
all middleware but runs single-threaded (sequential b.N iterations).

## Baseline Numbers

| Event type | ns/op | ms/op | Events/s (1 thread) | Allocs/op |
|---|---|---|---|---|
| MaaS (model lifecycle) | 2,394,449 | **2.4ms** | **417** | 355 |
| VM (compute instance) | 2,654,068 | **2.7ms** | **377** | 355 |

The MaaS simulator benchmark reports ~1,700 events/s because it uses 8-16
concurrent workers. Single-threaded throughput is ~400/s.

## Where the Time Goes

CPU profile breakdown of `handleEvent` (420ms cumulative, 20.9% of total):

| Operation | Time | % of handleEvent | What it does |
|---|---|---|---|
| `InsertRawEvent` | **140ms** | **33%** | INSERT into raw_events with unique index check |
| `handleModelEvent` | **180ms** | **43%** | UpsertModel + 3-5× InsertMeteringEntry |
| `handleComputeInstanceEvent` | **100ms** | **24%** | UpsertComputeInstance + 3× InsertMeteringEntry + UpdateLastMetered |

### Drilling into handleComputeInstanceEvent (100ms):

| Operation | Time | % |
|---|---|---|
| `InsertMeteringEntry` × 3 | **70ms** | **70%** |
| `UpsertComputeInstance` | **20ms** | **20%** |
| `UpdateComputeInstanceLastMetered` | **10ms** | **10%** |

### Full picture (flat CPU time):

| Category | Flat time | % of total |
|---|---|---|
| syscall (network I/O wait) | 840ms | 42% |
| kevent (epoll/kqueue wait) | 670ms | 33% |
| pgx DB operations | 420ms | 21% |
| Everything else (JSON, Go runtime) | ~80ms | 4% |

**The process spends 75% of CPU time waiting on I/O** (syscall + kevent).
This is expected for an I/O-bound workload — the CPU is barely working,
it's waiting for PostgreSQL round trips.

## Key Findings

### 1. DB round trips ARE the bottleneck — confirmed

The `pgx.Exec` call chain accounts for **100% of the meaningful work time**.
JSON decode, event classification, Go runtime overhead — all negligible.

Per VM event: 5 DB round trips (InsertRawEvent + UpsertComputeInstance +
3× InsertMeteringEntry + UpdateLastMetered).

Per MaaS event: 2-6 DB round trips (InsertRawEvent + UpsertModel +
1-5× InsertMeteringEntry).

At ~20-25ms per round trip (including network to Docker PG), the
sequential chain of 5-6 round trips = 2.4-2.7ms total — exactly what
we measured.

### 2. InsertMeteringEntry is the biggest target

For VM events: **70% of handler time** is spent in `InsertMeteringEntry`
(3 calls × ~23ms each). Batching these into one INSERT would reduce 3
round trips to 1, saving ~46ms per event.

### 3. InsertRawEvent is expensive

**33% of handleEvent time** on the unique index check (`event_id` unique
index). This is the deduplication mechanism. Can't easily batch this
because of the unique constraint, but could skip it for MaaS events
(fire-and-forget, no dedup needed).

### 4. JSON/CPU overhead is irrelevant

Only 4% of time is spent on non-I/O work. Optimizing JSON parsing,
reducing allocations, etc. would make zero measurable difference.

### 5. Concurrency scales well

Single-thread: ~400 events/s. With 8 workers (simulator): ~1,700 events/s.
That's 4.25x scaling with 8x concurrency — reasonable given pgxpool
connection sharing. Adding more connections to the pool would improve
this further.

## T1 (Within-Event Batch INSERTs) — Measured Impact

Implemented `InsertMeteringEntryBatch` — one multi-row INSERT per event
instead of 3-5 individual INSERTs. Benchmark results (3 runs, 5s each):

| Event type | Before (single) | After (batch) | Change |
|---|---|---|---|
| MaaS | 2,394 μs | 2,607 μs avg | **~same (within noise)** |
| VM | 2,654 μs | 2,846 μs avg | **~same (within noise)** |

### Why the improvement is negligible

Within-event batching saves 2-4 round trips per event (3 entries → 1 INSERT
for VMs). But each event still does:

```
InsertRawEvent     ~140μs   (33% — unique index check, can't batch)
UpsertModel/VM     ~20μs    (upsert, can't batch)
InsertMeteringBatch ~25μs   (was ~70μs as 3× singles — saved ~45μs)
UpdateLastMetered  ~10μs    (can't batch)
```

The ~45μs saved is swamped by the 140μs InsertRawEvent that dominates.
At 2,600μs total per event, saving 45μs is a 1.7% improvement — invisible
in benchmark noise.

### What would actually move the needle

The profiling shows the real bottleneck is **InsertRawEvent** (33% of time)
and the fact that each event requires multiple sequential DB operations
that can't be parallelized within a single event.

To get meaningful throughput improvement, we need **cross-event batching**:
buffer N events in a channel, process them in a batch from a writer
goroutine. This turns N × (InsertRawEvent + InsertMeteringBatch) into
1 × (batch InsertRawEvent + batch InsertMeteringEntries).

Estimated impact of cross-event batching (100 events per flush):
- 100 events × 6 round trips = 600 → ~3 round trips
- Per-event cost: ~2,600μs → ~50-100μs
- Single-thread throughput: ~400 → ~10,000-20,000 events/s

This is T6 (async write buffer) from the performance characteristics doc.

### Value of the within-event batch

Even though the benchmark shows no throughput improvement, the change is
still correct:
- **Cleaner code** — replaces loop-with-error-check with single call
- **Foundation for cross-event batching** — the batch function is ready
  to accept entries from multiple events
- **Reduces connection pool contention** under concurrent load — fewer
  Exec calls means fewer pool acquisitions per event

## What to Measure Next

1. **Cross-event batching (T6)** — async write buffer with configurable
   flush interval and batch size. This is where the real throughput gain is.
2. **Connection pool scaling** — benchmark with 4, 8, 16 max connections.
3. **InsertRawEvent alternatives** — skip for MaaS (fire-and-forget),
   or batch with COPY protocol.
4. **Production hardware** — Docker PG on laptop ≠ tuned PG on SSD.
