# API Reference

The API is defined by the [OpenAPI 3.0.3 specification](openapi.yaml).

[**Interactive API docs**](https://redocly.github.io/redoc/?url=https://raw.githubusercontent.com/myersCody/cost_ai_grid_poc/main/docs/openapi.yaml) — live from the spec on main.

## Quick Reference

### Main API Server (default `localhost:8020`)

| # | Method | Path | Description |
|---|--------|------|-------------|
| 1 | GET | `/healthz` | Kubernetes liveness probe |
| 2 | GET | `/readyz` | Kubernetes readiness probe (checks DB) |
| 3 | GET | `/api/v1/debug/config` | Diagnostic configuration (secrets masked) |
| 4 | POST | `/api/v1/events` | Ingest CloudEvents (VMaaS, CaaS, MaaS, IPP, custom) |
| 5 | GET | `/api/v1/rates` | List rate cards (JSON or CSV) |
| 6 | POST | `/api/v1/quotas` | Create a quota or budget |
| 7 | GET | `/api/v1/quotas` | List all active quotas (with optional status enrichment) |
| 8 | PUT | `/api/v1/quotas/{id}` | Update a quota |
| 9 | DELETE | `/api/v1/quotas/{id}` | Soft-delete a quota |
| 10 | GET | `/api/v1/quotas/{tenant_id}` | Quota consumption status for a tenant |
| 11 | POST | `/api/v1/wallets` | Create a prepaid wallet |
| 12 | GET | `/api/v1/wallets/{id}` | Wallet balance and status |
| 13 | POST | `/api/v1/wallets/{id}/top-ups` | Add funds to a wallet |
| 14 | POST | `/api/v1/wallets/{id}/adjustments` | Manual balance adjustment |
| 15 | GET | `/api/v1/wallets/{id}/ledger` | Wallet transaction audit trail |
| 16 | GET | `/api/v1/reports/costs` | Aggregated cost report (Koku-compatible, JSON or CSV) |
| 17 | GET | `/api/v1/reports/breakdown` | Per-resource cost line items (JSON or CSV) |
| 18 | GET | `/api/v1/reports/summary` | Pipeline health summary |
| 19 | GET | `/api/v1/customers/{id}/entitlements/{key}/value` | IPP-compatible balance check |
| 20 | POST | `/api/v1/reconcile` | Trigger manual OSAC reconciliation |
| 21 | GET | `/debug/dashboard` | Built-in diagnostic dashboard (HTML) |

### Metrics Server (separate port, no auth)

| Method | Path | Port | Description |
|--------|------|------|-------------|
| GET | `/metrics` | `METRICS_PORT` (default `9000`) | Prometheus metrics in text exposition format |

## OSAC Data Sources (non-HTTP)

The service also consumes data from the OSAC fulfillment-service via Watch stream and List APIs:

| Source | Path | Description |
|--------|------|-------------|
| OSAC REST/gRPC | `Watch` stream | Real-time SSE/gRPC event stream |
| OSAC REST | `GET /api/fulfillment/v1/compute_instances` | List VMs (reconciliation) |
| OSAC REST | `GET /api/fulfillment/v1/clusters` | List clusters (reconciliation) |
| OSAC REST | `GET /api/fulfillment/v1/instance_types` | List instance types |
| OSAC REST | `GET /api/fulfillment/v1/projects` | List projects |

See [gRPC Messages Catalog](grpc-messages-catalog.md) for message definitions.
