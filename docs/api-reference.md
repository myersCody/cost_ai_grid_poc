# API Reference

> HTTP endpoints exposed by the inventory-watcher when started with
> `INGEST_LISTEN_ADDR` (e.g., `localhost:8020`).
>
> Handler implementation: [`internal/ingest/handler.go`](../inventory-watcher/internal/ingest/handler.go)

## Endpoints

| Method | Path | Description | Handler | Tests |
|---|---|---|---|---|
| GET | `/api/v1/health` | Health check | inline in `ServeMux` | `TestHealthEndpoint` |
| POST | `/api/v1/events` | Ingest CloudEvents (VM, Cluster, MaaS, IPP) | `handleEvent` | `TestIngestMaaSEvent`, `TestIngestVMHeartbeat`, `TestIngestClusterHeartbeat`, `TestIngestIPPAuthoritativeFormat`, `TestIngestVMaaSAuthoritativeFormat`, `TestIngestCaaSAuthoritativeFormat` |
| GET | `/api/v1/quotas/{tenant_id}` | Quota status with alerts | `handleQuotaStatus` | `TestQuotaStatus`, `TestQuotaStatusMissingTenant`, `TestQuotaStatusWithConsumption` |
| GET | `/api/v1/reports/costs` | Cost report (JSON/CSV, group by tenant/type/meter/resource) | `handleCostReport` | — |
| GET | `/api/v1/reports/summary` | Pipeline health counts | `handlePipelineSummary` | — |
| GET | `/api/v1/customers/{id}/entitlements/{key}/value` | Balance check (IPP compatible) | `handleBalanceCheck` | `TestBalanceCheckResponseFormat` |
| GET | `/api/v1/debug/config` | Diagnostic config (secrets masked) | `handleDebugConfig` | — |
| GET | `/debug/dashboard` | Built-in diagnostic dashboard (HTML) | `handleDebugDashboard` | — |

**Test file:** [`internal/ingest/handler_test.go`](../inventory-watcher/internal/ingest/handler_test.go)
**Run:** `TEST_DB_URL=postgres://user:pass@localhost:5434/costdb_test go test ./internal/ingest/ -v`

---

## GET /api/v1/health

Health check. Returns 200 if the service is running.

**Response:** `200 OK`
```json
{"status":"ok"}
```

---

## POST /api/v1/events

Ingest a MaaS CloudEvent. Processes through the full pipeline:
raw_events → inventory_model → metering_entries.

**Handler:** [`handleEvent`](../inventory-watcher/internal/ingest/handler.go) (line 62)

### Request

**Content-Type:** `application/json`

**Body:** CloudEvents 1.0 structured format

```json
{
  "specversion": "1.0",
  "type": "osac.model.lifecycle",
  "source": "maas-simulator",
  "id": "unique-event-id",
  "time": "2026-06-26T10:00:00Z",
  "subject": "tenant-acme",
  "datacontenttype": "application/json",
  "data": {
    "tenant_id": "tenant-acme",
    "model_id": "model-llama-3-8b",
    "model_name": "llama-3-8b",
    "template": "osac.templates.maas_small",
    "state": "MODEL_STATE_RUNNING",
    "tokens_in": 15000,
    "tokens_out": 8000,
    "requests": 42,
    "duration_seconds": 60
  }
}
```

### Request Body Fields

**CloudEvents envelope:**

| Field | Type | Required | Description |
|---|---|---|---|
| `specversion` | string | Yes | Always `"1.0"` |
| `type` | string | Yes | Event type (e.g., `"osac.model.lifecycle"`) |
| `source` | string | Yes | Event producer identifier |
| `id` | string | Yes | Unique event ID (used for deduplication) |
| `time` | ISO 8601 | Yes | Event timestamp |
| `subject` | string | Yes | Tenant ID |
| `datacontenttype` | string | No | Always `"application/json"` |

**`data` payload:**

| Field | Type | Required | Description |
|---|---|---|---|
| `tenant_id` | string | Yes | Tenant identifier |
| `model_id` | string | Yes | Unique model deployment ID |
| `model_name` | string | Yes | Human-readable model name (e.g., `"llama-3-8b"`) |
| `template` | string | No | MaaS template reference |
| `state` | string | Yes | Model state (metered only when `"MODEL_STATE_RUNNING"`) |
| `tokens_in` | int | Yes | Input tokens processed in this interval |
| `tokens_out` | int | Yes | Output tokens generated in this interval |
| `requests` | int | Yes | Number of inference requests |
| `duration_seconds` | int | Yes | Interval length in seconds |

### Responses

| Status | Body | Meaning |
|---|---|---|
| `202 Accepted` | `{"status":"accepted"}` | Event processed successfully |
| `400 Bad Request` | `{"error":"..."}` | Malformed JSON |
| `409 Conflict` | `{"status":"duplicate"}` | Event ID already exists (deduplicated) |
| `500 Internal Server Error` | `{"error":"..."}` | Database or processing error |

### Processing Pipeline

On success, the event is processed through:
1. **raw_events** — stored immutably ([`InsertRawEvent`](../inventory-watcher/internal/inventory/store.go))
2. **inventory_model** — upserted ([`UpsertModel`](../inventory-watcher/internal/inventory/store.go))
3. **metering_entries** — 4 entries created ([`MeterMaaSEvent`](../inventory-watcher/internal/metering/metering.go)):
   `maas_tokens_in`, `maas_tokens_out`, `maas_requests`
4. **cost_entries** — created asynchronously by the rating sweep (every 30s)

---

## GET /api/v1/quotas/{tenant_id}

Returns quota consumption status for a tenant in the current monthly period.

**Handler:** [`handleQuotaStatus`](../inventory-watcher/internal/ingest/handler.go) (line 122)

### Parameters

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `tenant_id` | path | string | Yes | Tenant identifier (e.g., `"tenant-acme"`) |

### Response

**Content-Type:** `application/json`

```json
{
  "tenant_id": "tenant-acme",
  "period": "2026-06",
  "quotas": [
    {
      "meter_name": "maas_tokens_in",
      "unit": "tokens",
      "limit": 5000000,
      "consumed": 4447988,
      "percentage": 88.96,
      "thresholds": {
        "50": true,
        "70": true,
        "90": false,
        "100": false
      },
      "alerts": [
        {
          "tenant_id": "tenant-acme",
          "meter_name": "maas_tokens_in",
          "threshold_pct": 50,
          "consumed": 4447988,
          "limit_value": 5000000,
          "period": "2026-06",
          "state": "firing",
          "fired_at": "2026-06-28T11:26:49Z"
        },
        {
          "tenant_id": "tenant-acme",
          "meter_name": "maas_tokens_in",
          "threshold_pct": 70,
          "consumed": 4447988,
          "limit_value": 5000000,
          "period": "2026-06",
          "state": "firing",
          "fired_at": "2026-06-28T11:26:49Z"
        }
      ]
    },
    {
      "meter_name": "vm_cpu_core_seconds",
      "unit": "core_seconds",
      "limit": 360000,
      "consumed": 23648,
      "percentage": 6.57,
      "thresholds": {
        "50": false,
        "70": false,
        "90": false,
        "100": false
      }
    }
  ]
}
```

### Response Fields

| Field | Type | Description |
|---|---|---|
| `tenant_id` | string | Tenant identifier |
| `period` | string | Current billing period (`YYYY-MM`) |
| `quotas` | array | One entry per active quota for this tenant |
| `quotas[].meter_name` | string | Meter this quota applies to |
| `quotas[].unit` | string | Unit of measurement |
| `quotas[].limit` | number | Quota limit for the period |
| `quotas[].consumed` | number | Current consumption (SUM of metering_entries) |
| `quotas[].percentage` | number | `consumed / limit × 100`, rounded to 2 decimals |
| `quotas[].thresholds` | object | Whether each threshold level has been reached |
| `quotas[].thresholds["50"]` | boolean | True if consumption ≥ 50% of limit |
| `quotas[].thresholds["70"]` | boolean | True if consumption ≥ 70% of limit |
| `quotas[].thresholds["90"]` | boolean | True if consumption ≥ 90% of limit |
| `quotas[].thresholds["100"]` | boolean | True if consumption ≥ 100% of limit |
| `quotas[].alerts` | array | Threshold alerts fired for this meter in this period (omitted if none) |
| `quotas[].alerts[].threshold_pct` | number | Threshold level that was crossed (50, 70, 90, or 100) |
| `quotas[].alerts[].consumed` | number | Consumption at the time the alert fired |
| `quotas[].alerts[].limit_value` | number | Quota limit at the time the alert fired |
| `quotas[].alerts[].period` | string | Billing period (`YYYY-MM`) |
| `quotas[].alerts[].state` | string | Alert state (`"firing"`) |
| `quotas[].alerts[].fired_at` | ISO 8601 | When the threshold was first crossed |

### Notes

- **Latency:** Sub-second — single `SUM()` query per meter with existing indexes
- **Period:** Always the current calendar month (1st to end of month, UTC)
- **Empty quotas:** If no quotas are defined for the tenant, returns `{"quotas": null}`
- **Source of truth:** Consumption is computed from `metering_entries` in real-time
- **Alerts scaling:** At most 4 alerts per meter per period (one per threshold
  level). With 6 meters, max 24 alerts per tenant per month — trivially small.
- **Performance note:** The threshold evaluation runs every 30s in the rating
  sweep and queries `SUM(value)` per tenant × per meter. With the current
  implementation this is O(tenants × meters) SQL queries per sweep. For >100
  tenants, this should be optimized to batch the SUM queries.

---

## Internal Endpoints (Not HTTP — Watch Stream)

These are not HTTP endpoints but data flows consumed from OSAC:

| Source | Path | Description | Client method |
|---|---|---|---|
| OSAC REST Gateway | `GET /api/private/v1/events/watch` | SSE/NDJSON event stream | [`WatchEvents`](../inventory-watcher/internal/osac/client.go) |
| OSAC REST Gateway | `GET /api/fulfillment/v1/compute_instances` | List VMs | [`ListComputeInstances`](../inventory-watcher/internal/osac/client.go) |
| OSAC REST Gateway | `GET /api/fulfillment/v1/clusters` | List clusters | [`ListClusters`](../inventory-watcher/internal/osac/client.go) |
| OSAC REST Gateway | `GET /api/fulfillment/v1/instance_types` | List instance types | [`ListInstanceTypes`](../inventory-watcher/internal/osac/client.go) |
| OSAC REST Gateway | `GET /api/fulfillment/v1/projects` | List projects | [`ListProjects`](../inventory-watcher/internal/osac/client.go) |

See [gRPC Messages Catalog](grpc-messages-catalog.md) for the full message definitions.
