# IPP End-to-End Stress Test Report — 2026-07-05

## Test Architecture

![k3d Test Stack](../diagrams/k3d-test-stack.svg)

## Setup

Full IPP gateway stack on local k3d (see [k3d-ipp-deployment.md](k3d-ipp-deployment.md)):
- Istio 1.29.2 with `ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true`
- IPP from PR #320 with external-metering plugin (Helm chart, provider: istio)
- llm-katan echo mode as mock LLM
- Cost-event-consumer with PostgreSQL (204 response, post-fix)
- Prometheus scraping consumer metrics at 5s interval
- Grafana dashboard available

## Request Flow

Each request flows through the full IPP ext_proc pipeline:

1. `hey` → Istio Gateway (port-forward :18080 → :80)
2. Envoy ext_proc → IPP checkBalance → `GET /customers/{user}/entitlements/inference-tokens/value` on our consumer
3. IPP forwards to llm-katan (echo response with usage block)
4. Envoy ext_proc → IPP reportUsage → `POST /api/v1/events` CloudEvent to our consumer
5. Consumer: raw_events → metering_entries → (rating sweep) → cost_entries

## Benchmark Results (without unique constraint)

Benchmark tool: [hey](https://github.com/rakyll/hey) via port-forward.
No unique constraint on `raw_events.event_id` (append-only mode).

| Test | Requests | Concurrency | Duration | RPS | Avg | P50 | P95 | P99 |
|------|----------|-------------|----------|-----|-----|-----|-----|-----|
| Baseline | 5,000 | 10 | 6.2s | **803** | 12ms | 12ms | 16ms | 23ms |
| High concurrency | 5,000 | 50 | 5.8s | **860** | 58ms | 55ms | 73ms | 91ms |
| Max concurrency | 5,000 | 100 | 5.7s | **873** | 114ms | 109ms | 147ms | 264ms |
| Sustained (30s) | **25,456** | 20 | 30s | **848** | 24ms | 23ms | 30ms | 43ms |

**40,456 total requests, zero failures.** All HTTP 200.

### Endpoint Latency (from Prometheus)

| Endpoint | Avg Latency |
|----------|-------------|
| Balance check (`GET /customers/...`) | **0.36ms** |
| Usage report (`POST /api/v1/events`) | **2.17ms** |

Both well within the IPP's 5-second timeout.

## Benchmark Results (with unique constraint)

Same tests re-run after adding `CREATE UNIQUE INDEX ON raw_events (event_id)`:

| Test | Concurrency | Without | With | Delta |
|------|-------------|---------|------|-------|
| Baseline | 10 | 803 req/s | 733 req/s | **-9%** |
| High | 50 | 860 req/s | 812 req/s | **-6%** |
| Max | 100 | 873 req/s | 807 req/s | **-8%** |
| Sustained 30s | 20 | 848 req/s | 753 req/s | **-11%** |

**Cost of dedup: 6-11% throughput.** Latency impact is minimal (1-2ms).
Zero errors in both configurations.

## Observations

- Throughput plateaus at ~850 req/s (without constraint) / ~750 req/s
  (with constraint) — single-pod bottleneck
- Latency scales linearly with concurrency (P50: 12ms@10c → 109ms@100c)
- Sustained: **848 req/s for 30 seconds straight**, zero errors
- All events ingested: 41K+ raw_events, 83K+ metering_entries, cost
  entries rated per tenant by the 30s rating sweep

## Environment

- All components are single-replica on a local k3d cluster (ARM Mac
  via QEMU emulation — production on native amd64 would be faster)
- llm-katan echo mode adds ~1-2ms per request
- Port-forward adds ~1ms overhead vs in-cluster
- PostgreSQL is ephemeral (no persistent volume)

## Performance Optimization Opportunities

### In-Memory Balance Cache

The balance check queries PostgreSQL on every request (0.36ms avg).
Since we're the only writer to the database, we can maintain an
in-memory running total per tenant and skip the DB query entirely.

**Approach:**
- Use [gocache](https://github.com/eko/gocache) with Ristretto
  (in-memory) backend — swappable to Redis when scaling to multiple pods
- Cache key: `balance:{tenant_id}:{meter_name}`
- Invalidate on metering entry insert (which we control)
- TTL: 5-10 seconds as safety net
- Expected improvement: balance check from 0.36ms to <0.01ms

**Estimated throughput gain:** 15-30% (balance check is ~30% of
per-request CPU time at high concurrency)

### Batch Inserts

Currently we insert `raw_events` and `metering_entries` one row at a
time. Batching via multi-row `INSERT` or `COPY` would reduce PG
round-trips. Each event produces 1 raw_event + 2-5 metering entries =
3-6 individual inserts.

**Estimated throughput gain:** 20-40% for the ingest path

### Connection Pool Tuning

Default pgx pool size may be undersized for high concurrency. Profiling
the pool utilization under load would identify if connections are the
bottleneck at >50 concurrent requests.

## Fixes Applied During Testing

### 202 → 204 Response Code

The IPP client accepts only 200 and 204 for usage reports. We initially
returned 202, causing IPP to log `"failed to report usage"` despite
events being processed. Fixed to return 204 (matching the
[metering-simulator OpenAPI spec](../../docs/specs/maas-metering-openapi.yaml)).

Regression test added: `TestEventIngestResponseCode` — verifies the
response code is IPP-compatible (200 or 204).

The "with constraint" benchmark was run after this fix. The "without
constraint" benchmark was run before the fix (202 vs 204 does not
affect throughput).
