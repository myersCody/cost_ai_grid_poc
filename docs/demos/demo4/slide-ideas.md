# Demo 4 — Slide Ideas

> **Note:** This is a draft from 2026-07-03. The final slides are in
> [demo-4.md](demo-4.md). Counts below reflect the state at that date.
>
> Covering work since demo 3 (July 1 evening → July 3).
> Mix of live demo, terminal screenshots, and slides.

---

## Slide 1: Status Update

**Format:** Slide (table)

Show the requirements status: **13 done / 3 partial / 0 not started**.
Highlight what moved since demo 3:

| What changed | From → To |
|---|---|
| REQ-13 Custom metrics | Not started → **Done** |
| Observability (P1+P2) | Missing → **Done** |
| CI pipeline | Missing → **Done** |
| POC-ENV deployment | Not started → **Partial** |
| Adversarial review | v1 (17 findings) → v3 (41 findings, 19 fixed) |

---

## Slide 2: Architecture Diagram

**Format:** Slide (diagram)

Updated pipeline diagram showing the new components:

```
OSAC Watch Stream ──→ Watcher ──→ Inventory DB
                                      ↓
                      Reconciler ──→ (drift correction)
                                      ↓
CloudEvents ────→ Ingest Handler ──→ raw_events
  (HTTP)          ├─ VM/Cluster handler
                  ├─ MaaS/IPP handler
                  └─ Custom metrics ←── config.json (NEW)
                                      ↓
                      Metering sweep → metering_entries
                      Rating sweep  → cost_entries
                                      ↓
                      Report API ──→ JSON/CSV
                      Quota API  ──→ threshold alerts
                                      ↓
                  :9000/metrics ──→ Prometheus (NEW)
                  /healthz, /readyz  → K8s probes (NEW)
                  LOG_FORMAT=json    → log aggregation (NEW)
```

---

## Slide 3: Custom Metrics (REQ-13)

**Format:** Live demo

1. Show the config file (`deploy/custom-metrics-example.json`)
2. Start the service with `CUSTOM_METRICS_CONFIG=deploy/custom-metrics-example.json`
3. Send a custom GPU CloudEvent:
   ```bash
   curl -X POST localhost:8020/api/v1/events \
     -H 'Content-Type: application/json' \
     -d '{
       "specversion": "1.0",
       "type": "osac.gpu.lifecycle",
       "source": "demo",
       "id": "demo-gpu-001",
       "time": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
       "data": {
         "instance_id": "gpu-i-demo-001",
         "tenant_id": "tenant-acme",
         "gpu_memory_gib_seconds": 245760.0,
         "gpu_compute_seconds": 3600.0,
         "duration_seconds": 3600
       }
     }'
   ```
4. Query metering entries — show the GPU meters appeared automatically
5. Point: **zero code changes needed for new metric dimensions**

**Screenshot needed:** None — this is live.

---

## Slide 4: Observability — Prometheus Metrics

**Format:** Live demo + screenshot

1. `curl localhost:9000/metrics | grep cost_consumer` — show counters
   ticking up during the demo
2. Key metrics to highlight:
   - `cost_consumer_events_processed_total` — event throughput
   - `cost_consumer_metering_sweep_duration_seconds` — sweep latency
   - `cost_consumer_live_compute_instances` — resource gauge
   - `cost_consumer_http_requests_total` — API traffic

**Screenshot needed:** Terminal output of `curl :9000/metrics` with
non-zero counters after running the pipeline for a few minutes.

---

## Slide 5: Observability — Structured Logging

**Format:** Screenshot

Start with `LOG_FORMAT=json` and show a few log lines:

```json
{"time":"2026-07-03T...","level":"INFO","msg":"http request","method":"POST","path":"/api/v1/events","status":202,"duration_ms":3,"request_id":"a1b2c3d4e5f6"}
{"time":"2026-07-03T...","level":"INFO","msg":"metering sweep complete","compute_instances":4}
{"time":"2026-07-03T...","level":"INFO","msg":"rating sweep complete","rated":12,"skipped":0}
```

Point: ready for OpenShift log aggregation (Loki, Splunk, CloudWatch).

**Screenshot needed:** Terminal with JSON log lines flowing during
active pipeline operation.

---

## Slide 6: Observability — K8s Probes + Graceful Shutdown

**Format:** Screenshot or quick live demo

1. `curl localhost:8020/healthz` → `{"status":"ok"}`
2. `curl localhost:8020/readyz` → `{"status":"ready"}`
3. Kill the database, `curl localhost:8020/readyz` → 503 `{"status":"not_ready"}`
4. Send SIGTERM → show "shutting down ingest server, draining in-flight requests" log

**Screenshot needed:** Terminal showing readyz returning 503 after DB
kill, and the graceful shutdown log sequence.

---

## Slide 7: CI Pipeline

**Format:** Screenshot

Show the GitHub Actions page with:
- All green checkmarks on recent PRs
- 4 jobs: Lint, Build, Test, Container Build
- Test job running with real PostgreSQL service container

**Screenshot needed:**
1. GitHub Actions runs page showing green wall
2. Expanded view of one run showing the 4 parallel jobs
3. (Optional) Test job log showing `go test -race` output with all tests passing

---

## Slide 8: Adversarial Review Process

**Format:** Slide (table + numbers)

Show the review progression:

| Version | Scope | Findings | Fixed | Open |
|---|---|---|---|---|
| v1 | Full codebase | 17 | 9 | 5 |
| v2 | Observability PR | +16 = 33 | +10 = 19 | 10 |
| v3 | Custom metrics PR | +8 = 41 | +3 = 22 | 11 |

Highlight categories: security (auth added, input validation),
correctness (panic recovery, NaN/Inf rejection), operational
robustness (probes, graceful shutdown, metrics).

Point: **systematic quality process, not ad-hoc fixes**.

---

## Slide 9: CRC/OpenShift Deployment (POC-ENV)

**Format:** Screenshot

Show the service running on CRC:
- `oc get pods -n cost-mgmt` — pod Running, probes passing
- `oc logs` — JSON-formatted structured logs
- Health probes green in OpenShift console

**Screenshot needed:** OpenShift console or terminal showing:
1. Pod status with Ready indicators
2. Pod details showing probe configuration
3. (Optional) `oc port-forward` + curl to prove API works

---

## Slide 10: What's Next

**Format:** Slide (bullet list)

Remaining work for PoC completion:

- **REQ-5** (Partial): Scheduled chargeback export (report API done, export TBD)
- **REQ-1a** (Partial): Verify "cluster orders" = Cluster entity in OSAC
- **POC-ENV** (Partial): Deploy OSAC alongside our service on CRC
- **Observability PR #9**: Merge into Cody's repo
- **Connect to Noy's dogfood environment**: Test against real RHOAI → IPP events
- **Grafana dashboard**: Pre-built panels for the Prometheus metrics we expose

---

## Screenshots To Capture

Before the demo, capture these screenshots:

- [ ] GitHub Actions: green wall of CI runs
- [ ] GitHub Actions: expanded run showing 4 jobs (Lint/Build/Test/Container)
- [ ] Terminal: `curl :9000/metrics | grep cost_consumer` with live data
- [ ] Terminal: JSON log lines during pipeline operation
- [ ] Terminal: `/readyz` returning 503 after DB kill
- [ ] Terminal: graceful shutdown log sequence on SIGTERM
- [ ] (If available) OpenShift console: pod status in cost-mgmt namespace
