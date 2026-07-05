---
marp: true
theme: default
paginate: true
style: |
  section {
    font-size: 1.4rem;
    padding-top: 30px !important;
  }
  h1, h2 {
    margin-top: 0 !important;
    margin-bottom: 0.4em !important;
  }
  section.lead h1 {
    font-size: 2.4rem;
  }
  section.lead h2 {
    font-size: 1.4rem;
    color: #555;
  }
  h1 { color: #1a3a5c; }
  h2 { color: #2c6fad; border-bottom: 2px solid #2c6fad; padding-bottom: 4px; }
  code { background: #f0f4f8; padding: 2px 6px; border-radius: 3px; }
  pre { background: #f0f4f8; }
  .columns { display: grid; grid-template-columns: 1fr 1fr; gap: 2rem; }
  table { font-size: 1.1rem; }
  blockquote { border-left: 4px solid #2c6fad; color: #444; }
  .done { color: #16a34a; font-weight: bold; }
  .partial { color: #d97706; font-weight: bold; }
  img { border-radius: 6px; box-shadow: 0 2px 8px rgba(0,0,0,0.15); }
---

<!-- _class: lead -->

# Cost Event Consumer
## Demo 4 — Observability, Custom Metrics & Tooling

Martin Povolny — July 2026

<!--
Narration: Welcome. This is our fourth demo — covering everything we built
since the live dashboard demo on July 1. Focus areas: custom metric
extraction (REQ-13), observability stack, CI pipeline, integration testing,
and tooling.
-->

---

## What's New Since Demo 3

| Area | Status |
|---|---|
| **REQ-13** Custom metric extraction | <span class="done">Done</span> — config-driven, zero code changes |
| **Observability** P1+P2 | <span class="done">Done</span> — Prometheus, probes, logging, shutdown |
| **CI pipeline** | <span class="done">Done</span> — 6 jobs incl. k3s integration test |
| **Integration test** | <span class="done">Done</span> — full OSAC + cost stack on k3s in CI |
| **Adversarial review** | v3 — 41 findings, 22 fixed |
| **Tooling** | Bruno collection + Grafana dashboard |

**Score: 13 done / 4 partial / 1 TBD** (of 18 requirements)

<!--
Narration: Since demo 3 we shipped custom metrics, full observability,
a CI pipeline with integration testing, and developer tooling. 13 of 18
requirements are done.
-->

---

## REQ-13: Custom Metrics — The Problem

OSAC will emit new CloudEvent types over time:
- GPU workloads, storage volumes, network traffic, ...
- Each with different fields to meter

Without REQ-13: **every new metric = code change + PR + deploy**

With REQ-13: **drop a JSON config, restart**

<!--
Narration: This is the most important functional feature we added.
OSAC is evolving — new resource types, new metrics. Without REQ-13,
every new dimension means a code change. With it, an operator drops
a JSON config file and the system meters it automatically.
-->

---

## REQ-13: How It Works

```json
{
  "custom_metrics": [{
    "event_type": "osac.gpu.lifecycle",
    "resource_type": "gpu_instance",
    "resource_id_field": "instance_id",
    "tenant_id_field": "tenant_id",
    "meters": [
      { "meter_name": "gpu_memory_gib_seconds",
        "value_field": "gpu_memory_gib_seconds",
        "unit": "gib_seconds" },
      { "meter_name": "gpu_compute_seconds",
        "value_field": "gpu_compute_seconds",
        "unit": "seconds" }
    ]
  }]
}
```

Rating, reporting, quotas — all work automatically.

<!--
Narration: The config maps an event type to a resource type and lists
which fields to extract as meters. The rating engine, report API, and
quota system all work on free-text meter names — so custom metrics flow
through the entire pipeline with zero code changes.
-->

---

## REQ-13: Live Demo

1. Show config file → `CUSTOM_METRICS_CONFIG=deploy/custom-metrics-example.json`
2. Open **Bruno** → click "Custom GPU Metric" → Send
3. Query metering entries → GPU meters appear
4. Wait 30s → cost entries created with dollar amounts
5. **No code was changed. No recompile. No redeploy.**

<!--
Narration: [Live demo] Open Bruno, show the CloudEvent catalog. Click
"Custom GPU Metric" — this fires an event type that has no hardcoded
handler. The custom metrics config extracts gpu_memory_gib_seconds and
gpu_compute_seconds automatically. Check the pipeline summary — meters
created. Wait for the rating sweep — costs in dollars.
-->

---

## Built-in Debug Dashboard

![bg right:55% fit](screenshots/cost-debug-dash-1.png)

- Real-time cost summary
- **$94.62** total across 4 tenants
- Infrastructure vs Supplementary split
- Group by tenant, resource type, meter, resource
- 74,992 metering entries → 74,959 cost entries

<!--
Narration: The built-in dashboard shows the pipeline in action. $94.62
in total cost, split across 4 tenants. The "shared" tenant has both
infrastructure ($7.39 from VMs) and supplementary ($70.45 from MaaS
tokens). Each tenant's cost is isolated.
-->

---

## Debug Dashboard: Environment

![bg right:55% fit](screenshots/cost-debug-dash-2.png)

- OSAC connection status
- Database connection (credentials masked)
- Processing intervals: reconcile 1h, metering 60s, rating 30s
- Service settings: auth, log level, ports

<!--
Narration: The Environment tab shows operational config. OSAC connection
URL, database (credentials masked), processing intervals, auth status.
This is served from the binary itself — no separate tool needed.
-->

---

## Observability: Grafana Dashboard

![bg right:55% fit](screenshots/grafana-dash-3.png)

`docker compose up -d` → `http://localhost:3000`

- 17 live VMs from OSAC
- Event throughput + HTTP request rate
- Metering and cost entry creation rates
- Sweep duration p99
- Auto-provisioned, auto-refreshing

<!--
Narration: The Grafana dashboard scrapes our Prometheus metrics on port
9000. You can see 17 live VMs, metering entries being created for both
compute instances and MaaS tokens, cost entries flowing from the rating
sweep. This starts with docker compose up — dashboard is pre-provisioned.
-->

---

## Observability: Metrics Detail

| Metric | Type | What |
|---|---|---|
| `events_processed_total` | Counter | Events by type + status |
| `metering_entries_created_total` | Counter | Meters by resource + name |
| `cost_entries_created_total` | Counter | Costs by type |
| `metering_sweep_duration_seconds` | Histogram | 60s sweep latency |
| `rating_sweep_duration_seconds` | Histogram | 30s sweep latency |
| `live_compute_instances` | Gauge | Active VMs |
| `http_requests_total` | Counter | API traffic |

Separate `:9000` port (no auth) — RHT pattern from Koku.

<!--
Narration: All metrics use the cost_consumer_ namespace. Counters for
events, metering entries, cost entries. Histograms for sweep duration.
Gauges for live resources. Served on a separate port without auth so
Prometheus can scrape without a JWT.
-->

---

## Observability: Logging & Probes

**Structured JSON logging** for OpenShift log aggregation:
```json
{"time":"...","level":"INFO","msg":"http request",
 "method":"POST","path":"/api/v1/events",
 "status":202,"duration_ms":3,"request_id":"a1b2c3d4"}
```

**Kubernetes probes** (auth-exempt):
- `/healthz` → liveness (always 200)
- `/readyz` → readiness (pings DB, returns 503 if down)

**Graceful shutdown** with 30s drain + panic recovery on all goroutines.

<!--
Narration: LOG_FORMAT=json for production. Every request gets a request
ID. Probe endpoints are exempt from JWT auth so Kubernetes can reach
them. Graceful shutdown drains in-flight requests. If a goroutine panics,
the error propagates to the errgroup and the pod restarts.
-->

---

## CI Pipeline + Integration Test

![bg right:55% fit](screenshots/integration-test-osac-in-k3s.png)

**CI (every PR):** lint, build, test, links, container

**Integration test (k3s):**
- Deploys full OSAC + cost stack
- Creates resources in OSAC
- Sends CloudEvents
- Waits for metering + rating sweeps
- Verifies: probes, metrics, cost entries, quota API
- **12/12 ALL PASSED** in 6 minutes

<!--
Narration: Every PR runs 6 CI jobs. The integration test deploys the
full stack — OSAC gRPC, REST gateway, OIDC mock, two PostgreSQL
instances, and our consumer — on k3s in GitHub Actions. Then it runs
12 end-to-end checks. All green.
-->

---

## Bruno: Clickable CloudEvent Catalog

![bg right:55% fit](screenshots/bruno-cost.png)

Committed to git — no cloud, no accounts.

- 6 CloudEvent types (VM, Cluster, MaaS, IPP, GPU, Storage)
- Cost report with editable query params
- Quota status, balance check, reconcile trigger
- Docs tab with valid parameter values
- Response: $10.21 cost for tenant-acme

<!--
Narration: Bruno is a local HTTP client like Postman but file-based —
the collection is committed to git. Each request has documentation with
valid parameter values. Click to fire, see the response. Great for demos
and for developers exploring the API.
-->

---

## Adversarial Review Process

| Version | Scope | Total | Fixed | Accepted |
|---|---|---|---|---|
| v1 | Full codebase | 17 | 9 | 4 |
| v2 | Observability | +16 = 33 | +10 = 19 | +0 = 4 |
| v3 | Custom metrics | +8 = 41 | +3 = 22 | +4 = 8 |

Key fixes: JWT auth, input validation, panic recovery,
NaN/Inf rejection, cardinality protection, graceful shutdown.

<!--
Narration: We run adversarial reviews on every major PR. 41 findings
total, 22 fixed, 8 accepted as known PoC limitations. The review
catches real issues — the safeGo panic bug would have caused silent
data loss in production.
-->

---

## MaaS End-to-End: IPP Integration Verified

Full inference metering pipeline running on local k3d:

![MaaS IPP Flow](../../diagrams/maas-ipp-flow.svg)

- **Istio 1.29.2** + IPP ext_proc (PR #320) + llm-katan (echo LLM)
- Balance check: our consumer called on every request → `hasAccess: true/false`
- Usage report: CloudEvent `inference.tokens.used` → metering entries → cost
- [Setup guide](../../dev/k3d-ipp-deployment.md) · [IPP overview](../../research/ipp-overview.md) · [MaaS flow](../../maas-flow.md)

<!--
Narration: We deployed the full OSAC AI gateway stack locally and proved
that our cost consumer works as a drop-in replacement for OpenMeter.
The IPP plugin calls our checkBalance and reportUsage endpoints — both
verified against the upstream source code and OpenAPI spec.
-->

---

## IPP Stress Test: 850 req/s, Zero Errors

![Test Architecture](../../dev/k3d-test-stack.svg)

| Test | Concurrency | RPS | P50 | P99 |
|------|-------------|-----|-----|-----|
| Baseline | 10 | **803** | 12ms | 23ms |
| High | 50 | **860** | 55ms | 91ms |
| Sustained 30s | 20 | **848** | 23ms | 43ms |

40K+ requests, 100% success. Unique constraint costs 6-11% throughput.
[Full report](../../dev/ipp-stress-test-2026-07-05.md) · [PR #25](https://github.com/myersCody/cost_ai_grid_poc/pull/25)

<!--
Narration: We hammered the pipeline with 40,000 requests at up to 100
concurrent connections. 850 requests per second sustained, zero failures.
Balance check averages 0.36ms, usage report 2.17ms. This is on a local
k3d cluster running on ARM Mac via QEMU — production would be faster.
-->

---

## What's Next

| Item | Status | Next step |
|---|---|---|
| REQ-5 Chargeback export | Partial | Scheduled export (API done) |
| REQ-7 Audit trail | Partial | Document raw_events coverage |
| MaaS tenant attribution | Designed | Confirm subscription → tenant mapping |
| Noy's dogfood | Ready to connect | Our endpoints are IPP-compatible |
| Performance | 850 req/s baseline | In-memory balance cache for >2x |

<!--
Narration: We're down to closing partial requirements and optimizing.
The MaaS tenant attribution mapping needs confirmation from the OSAC
team. We're ready to connect to Noy's dogfood environment — our
endpoints match the IPP contract. Performance optimization next:
in-memory balance caching could double throughput.
-->

---

<!-- _class: lead -->

# Questions?

`github.com/myersCody/cost_ai_grid_poc`
