# Cost AI Grid PoC — Technical Guide

> Entry point for developers and stakeholders. Start here.

## What This Is

A proof-of-concept cost management system that integrates with
[OSAC](https://github.com/osac-project/fulfillment-service) (Open Sovereign
AI Console) to track infrastructure and AI model costs on a sovereign cloud
platform. Built as a standalone Go service outside of
[Koku](https://github.com/project-koku/koku).

**Deadline:** July 31, 2026

## How It Works

```
OSAC fulfillment-service
    │
    ├── gRPC Watch stream ──► Watcher ──► raw_events ──► inventory tables
    │                                                         │
    │                                            metering sweep (60s)
    │                                                         │
    │                                                  metering_entries
    │                                                         │
    │                                             rating sweep (30s)
    │                                                         │
    └── REST List endpoints ──► Reconciler              cost_entries
                                                              │
HTTP ingest endpoint ──► MaaS / custom events ──►   quota status API
         │                                           report API (JSON/CSV)
         └── custom metrics config (JSON) ──► arbitrary dimensions
```

## Quick Start

### Run locally (development)
1. [Local dev setup](dev/local-dev-setup.md) — run OSAC + the consumer locally
2. [Demo data setup](../snippets/setup-demo-data.sh) — create VMs, fire MaaS events, populate costs
3. [Query costs](../snippets/query-costs.sh) — see cost breakdowns by tenant, resource, model
4. [Bruno collection](../bruno-collection/) — clickable CloudEvent catalog (open in Bruno)
5. [Grafana stack](../deploy/observability/) — `docker compose up -d` for dashboards

### Deploy to OpenShift (CRC)
1. [Full CRC deployment guide](dev/crc-full-deployment.md) — **start here** for OpenShift deployment
   - Steps 1–8: full stack (cert-manager, CloudNativePG, OSAC, cost-event-consumer)
   - Step 9 (optional): MaaS inference metering (Istio + IPP + llm-katan)
2. [CRC checklist](dev/crc-deployment-checklist.md) — quick reference
3. [Troubleshooting](dev/troubleshooting.md) — common issues (cert SAN, token refresh, migrations)

## Architecture & Design

| Document | What you'll learn |
|---|---|
| [Architecture](poc_architecture/architecture.md) | Overall system architecture and data flow |
| [Data model](data-model.md) | All tables, ERD diagrams, Go model links, meter definitions |
| [gRPC messages catalog](grpc-messages-catalog.md) | Every OSAC proto message we consume |
| [CloudEvents catalog](cloudevents-catalog.md) | CloudEvent types, formats, authoritative sources |
| [API reference](api-reference.md) | HTTP endpoints, probes, metrics server |
| [Observability](observability.md) | Prometheus metrics, K8s probes, structured logging, graceful shutdown |

## Requirements & Status

| Document | What you'll learn |
|---|---|
| [Implementation status](implementation-status.md) | Every requirement with JIRA link, rank, status, and code links |
| [Requirements overview](requirements/poc_requirements_overview.md) | Canonical spec (v1.2) with priority ranking |
| [Requirements comparison](requirements/requirements-comparison.md) | Updated spec vs original brief |
| [OSAC open questions](requirements/osac-open-questions.md) | 23 open questions for the OSAC team |
| [Req #1 gap analysis](requirements/req1-osac-integration-gap-analysis.md) | OSAC integration |
| [Req #2 gap analysis](requirements/req2-maas-costing-gap-analysis.md) | MaaS token metering |
| [Req #8 gap analysis](requirements/req8-bare-metal-gap-analysis.md) | Bare metal costing |
| [Req #10 analysis](requirements/req10-threshold-notifications-analysis.md) | Threshold notifications |

## Architecture Decisions

| ADR | Decision |
|---|---|
| [ADR-001](decisions/001-metering-sweep-interval.md) | 60-second metering sweep |
| [ADR-002](decisions/002-arguments-against-kafka.md) | No Kafka — gRPC Watch + List reconciliation |
| [ADR-003](decisions/003-heartbeat-emitter-vs-sweep.md) | Local sweep replaces heartbeat collector |
| [ADR-004](decisions/004-raw-events-no-unique-index.md) | Drop unique index on raw_events for throughput |

## Research

| Document | Topic |
|---|---|
| [Rating engine options](research/rating-engine-options.md) | CloudKitty, GoRules/Zen, Drools evaluation |
| [REQ-13 custom metrics design](research/req13-custom-metrics-design.md) | Config-driven metric extraction |
| [Koku rate schema alignment](research/koku-rate-schema-alignment.md) | Rate table alignment with Koku |
| [Koku report API schema](research/koku-report-api-schema.md) | Report format alignment |
| [MaaS tenant attribution](research/maas-tenant-attribution.md) | Tenant routing for IPP events |
| [IPP overview](research/ipp-overview.md) | Inference Performance Protocol |
| [Metering approaches](research/metering-approaches-comparison.md) | Sweep vs event-driven comparison |

## Reviews

| Document | Scope |
|---|---|
| [Adversarial review v1](reviews/adversarial-review-v1.md) | Full codebase — 17 findings |
| [Adversarial review v2](reviews/adversarial-review-v2.md) | Observability PR — 33 total |
| [Adversarial review v3](reviews/adversarial-review-v3.md) | Custom metrics PR — 41 total, 22 fixed |
| [Adversarial review v4](reviews/adversarial-review-v4.md) | Full re-audit — 72 total |

## Demos

| Scenario | What it shows |
|---|---|
| [Demo 1: Full Pipeline](demos/demo-scenario-1.md) | Events → inventory → metering → cost → quotas → MaaS (11 acts) |
| [Demo 2: MaaS deep dive](demos/demo-scenario-2-maas.md) | Per-tenant and per-model cost drill-down, simulator options, throughput (extends demo 1 Act 11) |
| [Demo 3: Dashboard](demos/demo-scenario-3-dashboard.md) | Live dashboard, CSV export, per-tenant pricing |
| [Demo 4](demos/demo4/) | Observability, custom metrics, CI, CRC |

## Code Map

```
inventory-watcher/
├── cmd/
│   ├── consumer/main.go              Entry point — wires all components
│   └── maas-simulator/main.go        MaaS event generator (test tool)
├── internal/
│   ├── osac/
│   │   ├── client.go                 OSAC REST/Watch stream client
│   │   └── types.go                  Go mappings for OSAC proto messages
│   ├── watcher/watcher.go            Real-time event consumer
│   ├── reconciler/reconciler.go      Periodic List-based drift correction
│   ├── metering/
│   │   ├── metering.go               60s sweep + MaaS event metering
│   │   └── billable.go               Billable state definitions
│   ├── rating/rating.go              Rate engine, tiered pricing, seeding
│   ├── inventory/
│   │   ├── store.go                  PostgreSQL schema + all queries
│   │   └── models.go                 All Go struct types
│   ├── ingest/handler.go             HTTP API (events, quotas, health, reconcile)
│   ├── custommetrics/custommetrics.go Config-driven metric extraction (REQ-13)
│   ├── metrics/metrics.go            Prometheus metric definitions
│   ├── metrics/middleware.go         HTTP metrics + request logging middleware
│   └── config/config.go              Environment variable configuration
└── scripts/
    ├── setup.sh                      Full local setup (DBs, certs, build)
    ├── oidc_server.py                OIDC discovery server for OSAC auth
    └── gen_token.py                  JWT token generator

bruno-collection/                     Clickable CloudEvent catalog (open in Bruno)
deploy/
├── observability/                    Prometheus + Grafana docker-compose
├── custom-metrics-example.json       REQ-13 example config
└── k8s/                             Kubernetes/CRC manifests

snippets/
├── demo-start.sh                     Start all services + show status map
├── create-test-data.sh               Populate OSAC with test VMs
├── setup-demo-data.sh                One-command demo environment
├── send-mock-maas-events.sh          Quick mock MaaS events
├── benchmark-maas.sh                 Throughput benchmark (1700 events/s)
├── query-costs.sh                    Cost report queries
└── test-inventory-watcher.sh         End-to-end test suite
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `OSAC_BASE_URL` | `http://localhost:8011` | OSAC REST gateway |
| `OSAC_TOKEN` | — | Bearer token for OSAC API |
| `OSAC_CA_CERT` | — | CA certificate path (if HTTPS) |
| `INVENTORY_DB_URL` | `postgres://user:pass@localhost:5434/costdb` | PostgreSQL connection |
| `INGEST_LISTEN_ADDR` | — | HTTP API (e.g., `localhost:8020`). Disabled if empty |
| `METRICS_PORT` | `9000` | Prometheus metrics (separate port, no auth) |
| `CUSTOM_METRICS_CONFIG` | — | Path to custom metrics JSON config (REQ-13) |
| `RECONCILE_INTERVAL` | `1h` | How often to reconcile against OSAC |
| `METERING_INTERVAL` | `60s` | Metering sweep interval |
| `RATING_INTERVAL` | `30s` | Rating sweep interval |
| `LOG_LEVEL` | `info` | Log verbosity: debug, info, warn, error |
| `LOG_FORMAT` | `text` | Log format: `text` or `json` |
| `AUTH_ISSUER_URL` | — | OIDC issuer URL. Auth disabled if empty |

## Port Map (Local Development)

| Service | Port |
|---|---|
| OSAC gRPC | 8010 |
| OSAC REST gateway | 8011 |
| OSAC OIDC server | 8013 |
| OSAC PostgreSQL | 5433 |
| Cost inventory PostgreSQL | 5434 |
| Ingest / quota / report API | 8020 |
| Prometheus metrics | 9000 |
| Prometheus UI | 9090 |
| Grafana dashboard | 3000 |
