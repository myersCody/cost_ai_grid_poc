# Performance Characteristics — cost-event-consumer

## Status

Performance requirements are **not yet defined**. This document estimates
the load profile from first principles and maps it to our measured
throughput, identifying gaps and scaling options.

Waiting on:
- Scale requirements from product management
- OSAC event rate documentation
- RHOAI MaaS throughput numbers

## What We Know

### Measured throughput (laptop, single instance)

From MaaS simulator benchmark (`snippets/benchmark-maas.sh`):

| Metric | Value |
|---|---|
| Sustained event ingest | **~1,700 events/s** |
| Per-event latency | **<1ms** |
| Metering sweep | 60s interval, <100ms per sweep |
| Rating sweep | 30s interval, <50ms per sweep |
| End-to-end SLA | Event → cost entry in **<90 seconds** |

This is on a MacBook Pro with PostgreSQL in Docker. Production hardware
would likely do better.

### ACM scale target

ACM supports up to **3,500 clusters** from a single hub cluster. This is
the confirmed baseline for OSAC deployments.

Source: [ACM 2.10 scale testing](https://developers.redhat.com/blog/2024/06/21/scale-testing-acm-210)
— validated 3,500+ SNO clusters with <0.7% failure rate.

### RHOAI / MaaS throughput

No hard numbers published for RHOAI MaaS event rates. vLLM (the model
serving runtime) benchmarks:
- **793 tokens/s** on standard configs (vs 41 for Ollama)
- **100+ concurrent requests** on H100 with 7B models
- **2-24x throughput** vs conventional serving

Source: [vLLM production deployment](https://introl.com/blog/vllm-production-deployment-inference-serving-architecture),
[vLLM anatomy](https://vllm.ai/blog/2025-09-05-anatomy-of-vllm)

### Real-world customer profiles (from Koku on-prem testing)

Production customer distribution (from COST-7641):

| Profile | % of customers | Clusters | Nodes | Pods |
|---|---|---|---|---|
| Small | 37% | 1 | 15 | ~750 |
| Medium | 35% | 2 | 49 | ~3,200 |
| Large | 21% | 7 | 133 | ~21,000 |
| XLarge | 6% | 23 | 346 | ~138,000 |

Note: These are OCP on-prem profiles. OSAC sovereign cloud deployments
may differ significantly — likely fewer clusters but with more VMs and
MaaS workloads per cluster.

### OSAC event cadence

From the requirements spec and OSAC metering collector design:
- Heartbeat interval: **10-30 seconds** (configurable)
- Watch stream: real-time lifecycle events (CREATE/UPDATE/DELETE)
- Reconciler: hourly full-list sync
- Processing SLA: **60 seconds** from event receipt to cost entry

---

## Load Estimation

These two workloads have fundamentally different characteristics and should
be analyzed separately.

### Case 1: Capacity-Based (VMs, clusters, bare metal)

**How it works:** OSAC metering collector emits heartbeat CloudEvents on a
timer (10-30s). Each heartbeat carries pre-computed `duration_seconds` and
resource specs. Our local 60s sweep does the same thing from inventory.
Either way, the event rate is **bounded by resource count / interval**.

| Assumption | Value |
|---|---|
| Clusters | 3,500 (ACM max) |
| VMs per cluster | 10 (conservative) |
| Total VMs | 35,000 |
| Heartbeat interval | 30s |
| Metering entries per VM heartbeat | 3 (uptime, cpu, memory) |
| Metering entries per cluster heartbeat | 2 (uptime, worker_node) |

| Source | Resources | Events/s | Metering entries/s |
|---|---|---|---|
| VM heartbeats | 35,000 | ~1,167 | ~3,500 |
| Cluster heartbeats | 3,500 | ~117 | ~233 |
| **Total** | | **~1,284** | **~3,733** |

**vs measured throughput:** 1,700 events/s → **1.3x headroom**

With local sweep (our current architecture), this is even simpler — the
sweep queries inventory and bulk-inserts metering entries every 60s. No
inbound event rate to handle; the bottleneck is purely DB write throughput
for the INSERT batch.

**Scaling is predictable:** linear with resource count, bounded by the
heartbeat interval. 10x more VMs = 10x more events/s.

### Case 2: MaaS (inference serving)

**How it works:** Completely different from capacity-based. The IPP
(Inference Platform Plugin) external-metering module emits **one
CloudEvent per completed inference request** — not batched, not periodic.
The event rate is driven by **user traffic to models**, which we do not
control.

Source: [MaaS simulators and metering endpoints](../inputs/2026-06-30-maas-simulators-metering-endpoints.md)
— confirmed per-request granularity from the OSAC/RHOAI team.

**Transport:** The IPP plugin fires HTTP POST to our
`POST /api/v1/events` endpoint (fire-and-forget). Kafka transport
discussed but not decided. The IPP also calls `GET /api/v1/customers/{id}/entitlements/{key}/value`
synchronously on every request for balance checks (<500ms SLA).

**Event structure:** Each event carries 2 token dimensions (prompt, completion) + request count + model + user identity.
Produces up to 3 metering entries per event.

#### MaaS load scenarios

The event rate depends on: number of models × requests per model.

| Scenario | Models | Req/s per model | Events/s | Entries/s |
|---|---|---|---|---|
| Dev/test | 5 | 1 | 5 | 25 |
| Small production | 10 | 5 | 50 | 250 |
| Medium production | 50 | 10 | 500 | 2,500 |
| Large production | 100 | 10 | 1,000 | 5,000 |
| Heavy production | 100 | 50 | 5,000 | 25,000 |
| Extreme | 1,000 | 10 | 10,000 | 50,000 |

**vs measured throughput:** 1,700 events/s is exceeded at medium production
and above.

**Key differences from capacity-based:**
- Event rate is **unbounded** — driven by user traffic, not resource count
- Events are **bursty** — inference traffic has peaks and valleys
- Each event also triggers a **synchronous balance check** (quota API)
- Transport is HTTP POST (no backpressure) — if we're slow, events queue
  at the sender or get dropped (fire-and-forget)

#### Balance check load (GET /api/v1/customers/{id}/entitlements/{key}/value)

The IPP calls this **on every inference request** before forwarding to
the model. Must respond in <500ms. At 1,000 req/s, that's 1,000 balance
checks/s — each reading from `metering_entries` and `quotas` tables.
This is a **read-heavy** load concurrent with the write-heavy event
ingestion.

### Combined view

| Scenario | Capacity events/s | MaaS events/s | Total | vs 1,700 |
|---|---|---|---|---|
| PoC demo | ~20 | ~5 | ~25 | **68x headroom** |
| Small deployment | ~100 | ~50 | ~150 | **11x headroom** |
| Medium deployment | ~500 | ~500 | ~1,000 | **1.7x headroom** |
| Large (3,500 clusters + MaaS) | ~1,284 | ~1,000 | ~2,284 | **0.74x — insufficient** |
| Extreme | ~1,284 | ~10,000 | ~11,284 | **0.15x — far insufficient** |

---

## Bottleneck Analysis

### Current architecture

```
CloudEvent → HTTP handler → InsertRawEvent → Classify → Process → InsertMeteringEntry
                                 ↓                              ↓
                            PostgreSQL                    PostgreSQL
                          (single INSERT)              (N INSERTs per event)
```

### Identified bottlenecks

| # | Bottleneck | Impact | Evidence |
|---|---|---|---|
| 1 | **Single-row INSERTs** | Each event = 1 raw_event + 2-3 metering_entries = 3-4 INSERTs. At 1,700 events/s = ~8,500 INSERTs/s. Approaching PostgreSQL single-connection limit. | `store.go:InsertMeteringEntry` — individual INSERT per call |
| 2 | **No batching** | Events processed and committed one at a time. Batch INSERTs would reduce round trips 10-100x. | `handler.go:handleEvent` — synchronous per-event processing |
| 3 | **Synchronous processing** | HTTP handler waits for all DB writes before responding. Decoupling would improve ingest throughput. | `handler.go:240-246` — sequential INSERT loop |
| 4 | **Single goroutine sweeps** | Metering and rating sweeps run sequentially per resource type. | `metering.go:sweep` — serial sweep of compute, cluster, bare metal |
| 5 | **No connection pool tuning** | pgxpool defaults may undersize for high concurrency. | `main.go` — `pgxpool.New(ctx, dbURL)` with no config |
| 6 | **Full event JSON stored** | `raw_events` stores the full CloudEvent JSON. At high volume, this table grows fast. | `store.go:InsertRawEvent` — stores `fullJSON` |

### What is NOT a bottleneck

- **CPU** — Go is efficient; event processing is I/O bound, not CPU bound
- **Memory** — events are small (<1KB), no in-memory accumulation
- **Network** — single-node deployment, no cross-network hops
- **Rating engine** — `ApplyRate` is a pure function, <1μs per call

---

## Scaling Options

Ordered by effort and impact:

| # | Option | Effort | Impact | When |
|---|---|---|---|---|
| 1 | **Batch INSERTs** — buffer metering entries, flush every 100ms or N rows | S | **5-10x** throughput | Before load testing |
| 2 | **Connection pool tuning** — increase pgxpool max conns, set min conns | S | **2x** under contention | Before load testing |
| 3 | **Parallel sweeps** — run metering/rating per resource_type concurrently | S | **3-4x** sweep speed | Before load testing |
| 4 | **Async write buffer** — decouple HTTP response from DB commit | M | **3-5x** ingest throughput | If batching insufficient |
| 5 | **Table partitioning** — partition metering_entries by period_start (monthly) | M | Query perf at scale | Before 3-month retention |
| 6 | **Daily cost summary table** — pre-aggregate cost_entries by day/tenant/resource_type/meter | S | **10-100x** report query speed | Before report API latency is noticeable |
| 7 | **Read replicas** — separate read (reports) from write (ingest) | L | Removes contention | Production |
| 8 | **Horizontal sharding** — partition by tenant_id | L | Linear scaling | If single-instance ceiling hit |
| 9 | **Kafka ingest** — decouple event receipt from processing | L | Backpressure + replay | If HTTP is the bottleneck |

### MaaS-Specific Scaling Techniques

The current per-event path does 4 sequential PostgreSQL round trips:

```
1× InsertRawEvent + 3× InsertMeteringEntry = 4 INSERTs per inference event
```

At ~1ms per round trip = 4ms per event = ~250 events/s per DB connection.
pgxpool parallelizes across connections to reach 1,700, but the per-event
overhead is the fundamental limit.

#### Technique 1: Batch Metering INSERTs

Buffer N metering entries in memory, flush as one multi-row INSERT:
```sql
INSERT INTO metering_entries (resource_type, resource_id, ...) VALUES
  ('model', 'llama-8b', ...), ('model', 'llama-8b', ...), ...
```

| Batch size | Round trips per event | Estimated events/s | Effort |
|---|---|---|---|
| 1 (current) | 6 | ~1,700 | — |
| 10 | 1.5 (amortized) | ~5,000-8,000 | S |
| 100 | 1.05 | ~10,000-15,000 | S |

**Implementation:** Writer goroutine drains a channel every 100ms or
when buffer reaches N entries. Flush with a single `INSERT ... VALUES`.
~50 lines of Go.

**Trade-off:** Adds up to 100ms latency before entries hit the DB. Fine
for metering (60s SLA), not for balance checks.

#### Technique 2: In-Memory Pre-Aggregation

Batch inference events by `(model_id, tenant_id)` over a short window,
then write one metering entry with summed token counts. Token counts are
additive — aggregation is lossless for billing.

```
1,000 events/s × 5 meters = 5,000 INSERTs/s    (without aggregation)
100 models × 5 meters / 5s window = 100 INSERTs/s  (with aggregation)
```

| Window | Models | Reduction factor | Writes/s | Effort |
|---|---|---|---|---|
| No aggregation | — | 1x | 5,000 | — |
| 1s window | 100 | 50x | ~100 | M |
| 5s window | 100 | 250x | ~20 | M |
| 1s window | 1,000 | 5x | ~1,000 | M |

**Implementation:** `sync.Map` keyed by `(model_id, tenant_id, meter_name)`,
accumulate value, flush every N seconds. ~100 lines of Go.

**Trade-off:** Loses per-request granularity in metering_entries. Billing
totals are identical, but you can't query "what did request X cost" — only
"what did model Y cost in this 5s window." For most billing use cases this
is fine. Individual request costs can still be derived from the raw_events
table.

#### Technique 3: Balance Check Caching

The `GET /api/v1/customers/{id}/entitlements/{key}/value` endpoint reads `quota + consumption` on every
inference request. At 1,000 req/s, that's 1,000 DB reads/s concurrent
with write load.

Cache the consumption sum per `(tenant_id, meter_name)` with a short TTL:

| TTL | Read DB queries/s (at 1,000 req/s) | Staleness | Effort |
|---|---|---|---|
| No cache (current) | 1,000 | 0 | — |
| 1s TTL | ~100 (per tenant×meter unique) | ≤1s | S |
| 5s TTL | ~20 | ≤5s | S |
| 10s TTL | ~10 | ≤10s | S |

**Implementation:** `sync.Map` or a small LRU cache in the handler.
~30 lines of Go. No external cache (Valkey/Redis) needed at this scale.

**Trade-off:** A user could slightly overshoot their quota (by up to TTL
seconds of consumption). At 5s TTL and 10 tokens/request, overshoot is
at most ~50 tokens — negligible for million-token quotas.

#### Technique 4: Skip raw_events for MaaS

The `raw_events` table stores the full CloudEvent JSON for audit.
At 1,000 MaaS events/s, that's 86.4M rows/day, ~50GB/day of JSON.

Options:
- **Skip entirely** — MaaS metering entries are the audit trail.
  Saves 1 INSERT per event. Effort: S.
- **Async bulk insert** — batch raw events into COPY, flush every 1s.
  Keeps the audit trail without per-event overhead. Effort: S.
- **Separate table** — `raw_events_maas` with simpler schema, no
  deduplication (dedup not needed for fire-and-forget events). Effort: S.

#### Technique 5: PostgreSQL COPY Protocol

Replace batch `INSERT ... VALUES` with `COPY ... FROM STDIN` (binary).
COPY bypasses the SQL parser and executor — it's the fastest bulk write
path in PostgreSQL.

| Method | Rows/s (typical PG) | vs batch INSERT |
|---|---|---|
| Single INSERT | ~10,000 | 1x |
| Batch INSERT (100 rows) | ~50,000-100,000 | 5-10x |
| COPY binary | ~200,000-500,000 | 20-50x |

**Implementation:** pgx supports `conn.CopyFrom()` natively. ~20 lines
to replace batch INSERT with COPY. Effort: S.

**Trade-off:** COPY is all-or-nothing per batch — no partial success.
Fine for metering entries (retry the whole batch on failure).

#### Technique 6: Async Write Buffer

Decouple HTTP response from DB commit entirely:

```
HTTP POST → validate → push to channel → return 202 immediately
                              ↓
                    Writer goroutine(s)
                    drain channel → batch → INSERT/COPY → DB
```

| Buffer depth | Max burst absorption | Sustained writes/s | Effort |
|---|---|---|---|
| 1,000 events | 1s burst at 1,000/s | limited by DB | M |
| 10,000 events | 10s burst at 1,000/s | limited by DB | M |
| 100,000 events | 100s burst at 1,000/s | limited by DB | M |

**Implementation:** Buffered Go channel + 1-N writer goroutines. ~100
lines. Combines naturally with techniques 1-2 (batch from the channel).

**Trade-off:** Events are acknowledged before persisted. If the process
crashes, buffered events are lost. Acceptable if Kafka transport provides
delivery guarantees upstream. Not acceptable for fire-and-forget HTTP
without replay capability.

### Combined Impact

Techniques compose — here's the estimated throughput stacking them:

| Configuration | Events/s | DB writes/s | Covers |
|---|---|---|---|
| **Current** | ~1,700 | ~10,200 | Capacity only |
| **+ Batch INSERTs** (T1) | ~8,000 | ~2,000 | + MaaS medium |
| **+ Pre-aggregation** (T1+T2) | ~50,000 | ~200 | + MaaS heavy |
| **+ Balance cache** (T1+T2+T3) | ~50,000 (read load eliminated) | ~200 | + concurrent balance checks |
| **+ COPY** (T1+T2+T3+T5) | ~100,000+ | ~200 | + MaaS extreme |
| **+ Async buffer** (all) | ~200,000+ burst | ~200 sustained | Burst absorption |

The sweet spot for production is **T1 + T2 + T3** (batch INSERTs +
pre-aggregation + balance cache). This handles 50,000 events/s with
~200 DB writes/s — well within PostgreSQL's comfort zone on modest
hardware. Total effort: M (a few hundred lines of Go, no new dependencies).

---

## Database Sizing

### Row size estimates

Estimated from schema column types + PostgreSQL tuple overhead (~24 bytes
header + alignment). Indexes add ~50-100% overhead for write-heavy tables.

| Table | Columns | Est. row size (data) | Est. row size (with indexes) |
|---|---|---|---|
| `raw_events` | 11 cols, JSONB data (~500B avg for MaaS, ~200B for capacity) | ~600-800B | ~1-1.5KB |
| `metering_entries` | 14 cols, all scalar | ~150B | ~300B |
| `cost_entries` | 14 cols, all scalar | ~180B | ~350B |

### Row counts per day

Every inbound event produces 1 raw_event row. Each event produces 2-5
metering entries. Each metering entry produces 1 cost entry (after rating).

| Scenario | Events/s | raw_events/day | metering_entries/day | cost_entries/day | Total rows/day |
|---|---|---|---|---|---|
| **Capacity only** (3,500 clusters, 35K VMs) | 1,284 | 111K | 322K | 322K | **755K** |
| **+ MaaS small** (10 models, 5 req/s) | 1,334 | 115K | 347K | 347K | **809K** |
| **+ MaaS medium** (50 models, 10 req/s) | 1,784 | 154K | 538K | 538K | **1.2M** |
| **+ MaaS heavy** (100 models, 50 req/s) | 6,284 | 543K | 2.5M | 2.5M | **5.5M** |
| **+ MaaS extreme** (1,000 models, 10 req/s) | 11,284 | 975K | 4.6M | 4.6M | **10.2M** |

With 5s pre-aggregation on MaaS metering entries:

| Scenario | raw_events/day | metering_entries/day | cost_entries/day | Total rows/day |
|---|---|---|---|---|
| **+ MaaS heavy (aggregated)** | 543K | 346K | 346K | **1.2M** |
| **+ MaaS extreme (aggregated)** | 975K | 496K | 496K | **2.0M** |

### Table sizes per day

| Scenario | raw_events | metering_entries | cost_entries | Total/day |
|---|---|---|---|---|
| Capacity only | ~110MB | ~97MB | ~113MB | **~320MB** |
| + MaaS medium | ~185MB | ~161MB | ~188MB | **~534MB** |
| + MaaS heavy (no agg.) | ~650MB | ~750MB | ~875MB | **~2.3GB** |
| + MaaS heavy (5s agg.) | ~650MB | ~104MB | ~121MB | **~875MB** |
| + MaaS extreme (no agg.) | ~1.2GB | ~1.4GB | ~1.6GB | **~4.2GB** |
| + MaaS extreme (5s agg.) | ~1.2GB | ~149MB | ~174MB | **~1.5GB** |

### Cumulative growth (3-month retention)

| Scenario | 1 month | 3 months | Notes |
|---|---|---|---|
| Capacity only | ~10GB | ~29GB | Comfortable on 50Gi PVC |
| + MaaS medium | ~16GB | ~48GB | Tight on 50Gi, needs 100Gi |
| + MaaS heavy (no agg.) | ~68GB | ~203GB | Needs 250Gi+ PVC, PG tuning |
| + MaaS heavy (5s agg.) | ~26GB | ~79GB | 100Gi PVC sufficient |
| + MaaS extreme (no agg.) | ~126GB | ~378GB | Needs partitioning + large storage |
| + MaaS extreme (5s agg.) | ~45GB | ~135GB | 200Gi PVC, manageable |

### Row counts at 3 months

| Scenario | raw_events | metering_entries | cost_entries | Total |
|---|---|---|---|---|
| Capacity only | 10M | 29M | 29M | **68M** |
| + MaaS medium | 14M | 48M | 48M | **110M** |
| + MaaS heavy (no agg.) | 49M | 225M | 225M | **499M** |
| + MaaS heavy (5s agg.) | 49M | 31M | 31M | **111M** |
| + MaaS extreme (no agg.) | 88M | 414M | 414M | **916M** |
| + MaaS extreme (5s agg.) | 88M | 45M | 45M | **178M** |

### PostgreSQL resource requirements

Based on table sizes and query patterns (analytical reads on cost_entries,
sequential scans on metering_entries for rating sweep):

| Deployment | Rows (3mo) | Storage | PG shared_buffers | PG work_mem | CPU | RAM |
|---|---|---|---|---|---|---|
| **Small** (capacity only) | 68M | 50Gi | 1GB | 64MB | 2 cores | 4Gi |
| **Medium** (+ MaaS agg.) | 111M | 100Gi | 2GB | 128MB | 4 cores | 8Gi |
| **Large** (heavy MaaS agg.) | 111M | 100Gi | 2GB | 128MB | 4 cores | 8Gi |
| **XL** (extreme MaaS agg.) | 178M | 200Gi | 4GB | 256MB | 8 cores | 16Gi |
| **XL** (extreme no agg.) | 916M | 500Gi+ | 8GB | 512MB | 16 cores | 32Gi |

### Query performance at scale

Critical queries and expected behavior at row counts above:

| Query | Used by | Rows scanned | At 100M rows | At 500M rows |
|---|---|---|---|---|
| `UnratedMeteringEntries` (LEFT JOIN cost_entries) | Rating sweep | Full scan of unrated | <100ms (indexed) | <500ms (indexed) |
| `MeteringSum` (SUM WHERE tenant + meter + period) | Balance check, quota | Index scan | <10ms | <50ms |
| `CostReport` (GROUP BY tenant/type/meter) | Report API | Period-bounded scan | <500ms | 1-5s (needs partitioning) |
| `InsertMeteringEntry` (single INSERT) | Event handler | 1 row | <1ms | <1ms |
| `InsertRawEvent` (INSERT + unique check) | Event handler | 1 row + index | <1ms | <2ms (index grows) |

**When to partition:** At ~200M+ rows in `metering_entries` or
`cost_entries`, range-partition by `period_start` (monthly). This keeps
report queries fast and enables cheap partition drops for retention.

### Index impact

Each table has 2-3 indexes. At high write rates, index maintenance
becomes significant:

| Table | Indexes | Write amplification | Impact at 5,000 INSERTs/s |
|---|---|---|---|
| `raw_events` | PK + event_id unique + tenant_time + type_time | ~4x | Moderate — unique check on event_id |
| `metering_entries` | PK + tenant_meter + resource | ~3x | High — 3 index updates per INSERT |
| `cost_entries` | PK + tenant_period | ~2x | Moderate |

Pre-aggregation reduces metering_entries INSERTs by 50-250x, which is
where most of the index maintenance cost sits.

---

## What We Need to Know

| # | Question | Why it matters | Status |
|---|---|---|---|
| 1 | How many VMs per cluster? | Drives capacity event rate linearly | Unknown — ask OSAC team |
| 2 | MaaS metering granularity? | Determines MaaS event rate | **Answered: per-request** (from IPP design) |
| 3 | How many models per deployment? | Scales MaaS event rate | Unknown — ask product management |
| 4 | Expected inference req/s per model? | Directly determines MaaS load | Unknown — ask RHOAI team |
| 5 | Is 3,500 clusters the real target? | Changes capacity load profile | Confirmed by ACM testing |
| 6 | Target data retention period? | DB growth rate and query perf | Unknown |
| 7 | Is federation acceptable? | Determines sharding requirement | Unknown — architecture decision |
| 8 | What hardware is the target? | Baseline for load testing | Unknown |
| 9 | Balance check latency SLA? | Determines read load on DB | **Answered: <500ms** (from IPP spec) |

---

## Comparison with Koku On-Prem Pipeline

Our architecture is fundamentally different from Koku on-prem:

| Aspect | Koku on-prem | cost-event-consumer |
|---|---|---|
| Language | Python (Celery workers) | Go |
| Transport | Kafka + S3 staging | gRPC Watch stream + HTTP |
| Processing | Batch (daily CSV uploads) | Real-time (per-event) |
| Data format | CSV → Parquet → DB | CloudEvent JSON → DB |
| Bottleneck | Celery queue + PostgreSQL summary SQL | PostgreSQL INSERT throughput |
| Processing SLA | 6-24 hours | 60 seconds |
| Max validated | ~50 clusters (testing in progress) | ~35,000 VMs (estimated) |

The Koku on-prem performance work (COST-7567, COST-7641) provides useful
**methodology** (sizing tiers, bottleneck hypotheses, soak tests) but the
specific findings don't apply — different pipeline, different bottlenecks,
different scale targets.

---

## Recommendation

### For the PoC demo (July 31)

Current throughput is sufficient. No optimization needed for the demo.

### Before production — phased approach

| Phase | Techniques | Effort | Unlocks |
|---|---|---|---|
| **Phase 0** (PoC) | None needed | — | ~1,700 events/s — capacity + light MaaS |
| **Phase 1** | Batch INSERTs (T1) + balance cache (T3) | S | ~8,000 events/s — capacity + medium MaaS |
| **Phase 2** | + Pre-aggregation (T2) | M | ~50,000 events/s — heavy MaaS |
| **Phase 3** | + COPY protocol (T5) + async buffer (T6) | M | ~100,000+ events/s — extreme |

The sweet spot is **Phase 2** — handles 50,000 events/s with only ~200 DB
writes/s. Total implementation: a few hundred lines of Go, no new
dependencies, no architectural changes.

### Key decisions needed

1. **Is per-request MaaS metering acceptable, or should we push for
   upstream aggregation?** If OSAC aggregates before sending, most of
   this optimization work becomes unnecessary.
2. **What's the target MaaS scale?** 10 models × 5 req/s (trivial) vs
   1,000 models × 10 req/s (needs Phase 2+) — very different answers.
3. **Is losing per-request granularity acceptable?** Pre-aggregation (T2)
   is the biggest win but trades per-request detail for per-window sums.

---

## Self-Review: Assumptions, Gaps, and Caveats

### Assumptions that could be wrong

| # | Assumption | If wrong | Impact |
|---|---|---|---|
| 1 | **10 VMs per cluster is "conservative"** | Many OSAC deployments may have 0 VMs (clusters only) or 100+ VMs. The 35K VM number could be off by 10x in either direction. | Capacity load estimate could be 10x too high or 10x too low |
| 2 | **1,700 events/s benchmark is representative** | Measured on a laptop with Docker PostgreSQL. Production PG on SSD with tuned `shared_buffers` could do 3-5x better. Or worse, if PG is on shared storage (ODF/Ceph). | Headroom estimates could be significantly off |
| 3 | **Each MaaS event produces up to 3 metering entries** | Depends on which token dimensions are non-zero. Most requests produce 2-3 (prompt + completion + sometimes reasoning). Rarely all 5. | MaaS entries/s overestimated by ~2x |
| 4 | **Balance check hits DB every time** | The `MeteringSum` query may already be fast enough (<1ms with warm cache) that caching isn't needed until very high concurrency. | T3 may be premature optimization |
| 5 | **Pre-aggregation loses only per-request granularity** | If billing disputes require per-request cost breakdown, pre-aggregation is unacceptable without the raw_events fallback. But raw_events at 1,000/s is its own scaling problem. | T2 may not be viable without T4 (raw_events handling) |
| 6 | **Capacity events arrive as HTTP heartbeats** | With our current architecture, capacity metering comes from the local sweep — NOT from inbound events. The sweep bulk-inserts all metering entries at once every 60s. This is a fundamentally different load profile from 1,284 HTTP events/s. | Capacity throughput concern is overstated — the sweep handles 35K resources comfortably |

### What the numbers do NOT account for

- **VACUUM overhead** — PostgreSQL autovacuum runs concurrently with inserts. At high write rates, vacuum lag causes table bloat, increasing scan times. Not modeled.
- **WAL write amplification** — every INSERT generates WAL entries. At 10K INSERTs/s with 3 indexes, WAL throughput could saturate disk I/O before CPU or memory. Not measured.
- **Connection pool contention** — pgxpool default max connections is based on CPU count. Under high concurrency (MaaS events + sweeps + rating + balance checks all hitting PG), connection wait times increase non-linearly. Not modeled.
- **Rating sweep interaction** — the `UnratedMeteringEntries` query does a LEFT JOIN on two growing tables. At 100M+ rows, even with the `idx_ce_metering` index, the anti-join pattern degrades. The query has no period filter — it scans from the oldest unrated entry. Not included in query performance table.
- **Burst behavior** — all estimates assume steady-state. Real inference traffic is bursty (e.g., batch jobs hitting a model at 10x the average rate for 5 minutes). Burst absorption depends on async buffering (T6) which has data loss trade-offs.
- **Multi-tenant query isolation** — report queries for one tenant may need to scan through all tenants' data if partitioning is by time, not tenant. Cross-tenant interference not modeled.
- **Network latency** — if PostgreSQL runs on a separate node (typical in OpenShift), each round trip adds ~0.5-1ms of network latency on top of query execution time. The 1ms/round-trip assumption may be 2ms in production.
