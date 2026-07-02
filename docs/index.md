# Cost AI Grid PoC — Technical Guide

> Entry point for developers and stakeholders. Start here.

## What This Is

A proof-of-concept cost management system that integrates with
[OSAC](https://github.com/osac-project/fulfillment-service) (Open Sovereign
AI Console) to track infrastructure and AI model costs on a sovereign cloud
platform. Built as a standalone Go service outside of
[Koku](https://github.com/project-koku/koku).

**Deadline:** July 31, 2026
**Branch:** `osac-cost-consumer`

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
HTTP ingest endpoint ──► MaaS events ──────────►    quota status API
```

The system consumes OSAC events in real time, builds an inventory of
infrastructure resources (VMs, clusters, models), meters their usage, applies
rates, and produces cost entries with dollar amounts. A quota API lets OSAC
check whether tenants are within their resource limits.

## Quick Start

1. [Local dev setup](local-dev-setup.md) — run OSAC + the consumer locally
2. [Demo data setup](../snippets/setup-demo-data.sh) — create VMs, fire MaaS events, populate costs
3. [Query costs](../snippets/query-costs.sh) — see cost breakdowns by tenant, resource, model

## Architecture & Design

| Document | What you'll learn |
|---|---|
| [Data model](data-model.md) | All 11 tables, ERD diagrams, Go model links, meter definitions |
| [gRPC messages catalog](grpc-messages-catalog.md) | Every OSAC proto message we consume, linked to [fulfillment-service protos](https://github.com/osac-project/fulfillment-service/tree/main/proto/public/osac/public/v1) |
| [API reference](api-reference.md) | HTTP endpoints we expose (health, event ingest, quota status) |
| [Architecture thoughts](thoughts.md) | Original design exploration: why events, how the pipeline works |
| [Cost reports feasibility](cost-reports-feasibility.md) | What reports we can provide vs what Koku does |

## Requirements & Status

| Document | What you'll learn |
|---|---|
| [Implementation status](implementation-status.md) | Every requirement with acceptance criteria, status, and code links |
| [Requirements comparison](requirements/requirements-comparison.md) | Updated spec vs original brief — what changed, what's ahead of schedule |
| [Req #1 gap analysis](requirements/req1-osac-integration-gap-analysis.md) | OSAC integration — inventory, metering, billable states |
| [Req #2 gap analysis](requirements/req2-maas-costing-gap-analysis.md) | MaaS token metering — consumption-based billing |
| [Req #8 gap analysis](requirements/req8-bare-metal-gap-analysis.md) | Bare metal costing — OSAC blockers and plan |
| [Req #10 analysis](requirements/req10-threshold-notifications-analysis.md) | Threshold notifications — delivery models and open questions |

**Spec references:**
- [Requirements overview](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md)

## Architecture Decisions

| ADR | Decision |
|---|---|
| [ADR-001](decisions/001-metering-sweep-interval.md) | 60-second metering sweep — matches OSAC collector interval and processing SLA |
| [ADR-002](decisions/002-arguments-against-kafka.md) | No Kafka — gRPC Watch + List reconciliation (Kubernetes pattern) provides same guarantees |

## Research

| Document | What you'll learn |
|---|---|
| [Rating engine options](research/rating-engine-options.md) | CloudKitty, GoRules/Zen, Drools — what we chose and why |

## Demos

| Scenario | What it shows |
|---|---|
| [Demo 1: Infrastructure](demo-scenario-1.md) | VM reconciliation, Watch stream, metering sweep, DELETE handling |
| [Demo 2: MaaS + Cost](demo-scenario-2-maas.md) | Token metering, cost calculation, per-tenant breakdown, throughput |

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
│   ├── ingest/handler.go             HTTP API (events, quotas, health)
│   └── config/config.go              Environment variable configuration
└── scripts/
    ├── setup.sh                      Full local setup (DBs, certs, build)
    ├── oidc_server.py                OIDC discovery server for OSAC auth
    └── gen_token.py                  JWT token generator

snippets/
├── setup-demo-data.sh                One-command demo environment
├── create-test-data.sh               Populate OSAC with test VMs
├── send-mock-maas-events.sh          Quick mock MaaS events via DB
├── benchmark-maas.sh                 Throughput benchmark (1700 events/s)
├── query-costs.sh                    Cost report queries
└── test-inventory-watcher.sh         End-to-end test suite (27 assertions)
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `OSAC_BASE_URL` | `http://localhost:8011` | OSAC REST gateway |
| `OSAC_TOKEN` | — | Bearer token for OSAC API |
| `OSAC_CA_CERT` | — | CA certificate path (if HTTPS) |
| `INVENTORY_DB_URL` | `postgres://user:pass@localhost:5434/costdb` | PostgreSQL connection |
| `RECONCILE_INTERVAL` | `1h` | How often to reconcile against OSAC |
| `SUMMARIZE_INTERVAL` | `1h` | Daily summary interval |
| `INGEST_LISTEN_ADDR` | — | HTTP ingest endpoint (e.g., `localhost:8020`). Disabled if empty. |
| `LOG_LEVEL` | `info` | Log verbosity |

## Port Map (Local Development)

| Service | Port |
|---|---|
| OSAC gRPC | 8010 |
| OSAC REST gateway | 8011 |
| OSAC PostgreSQL | 5433 |
| Cost inventory PostgreSQL | 5434 |
| Ingest/quota API | 8020 |
| OSAC OIDC server | 8013 |
