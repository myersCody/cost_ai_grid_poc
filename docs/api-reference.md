# API Reference

> **Service:** cost-event-consumer
> **Version:** v1
> **Base URL:** `http://{INGEST_LISTEN_ADDR}` (default `localhost:8020`)
> **Metrics URL:** `http://:{METRICS_PORT}` (default port `9000`)
>
> Handler implementation: [`internal/ingest/handler.go`](../inventory-watcher/internal/ingest/handler.go)
> Response types: [`internal/inventory/models.go`](../inventory-watcher/internal/inventory/models.go)

The cost-event-consumer exposes 21 HTTP endpoints across two servers: the main
API server (authenticated via JWT when `AUTH_ISSUER_URL` is set) and a metrics
server on a separate port (unauthenticated).

---

## Endpoint Summary

### Main API Server

| # | Method | Path | Description |
|---|--------|------|-------------|
| 1 | GET | `/healthz` | Kubernetes liveness probe |
| 2 | GET | `/readyz` | Kubernetes readiness probe |
| 3 | GET | `/api/v1/debug/config` | Diagnostic configuration (secrets masked) |
| 4 | POST | `/api/v1/events` | Ingest CloudEvents (VMaaS, CaaS, MaaS, IPP, custom) |
| 5 | GET | `/api/v1/rates` | List rate cards (JSON or CSV) |
| 6 | POST | `/api/v1/quotas` | Create a quota |
| 7 | GET | `/api/v1/quotas` | List all active quotas |
| 8 | PUT | `/api/v1/quotas/{id}` | Update a quota |
| 9 | DELETE | `/api/v1/quotas/{id}` | Soft-delete a quota |
| 10 | GET | `/api/v1/quotas/{tenant_id}` | Quota consumption status for a tenant |
| 11 | POST | `/api/v1/wallets` | Create a prepaid wallet |
| 12 | GET | `/api/v1/wallets/{id}` | Wallet balance and status |
| 13 | POST | `/api/v1/wallets/{id}/top-ups` | Add funds to a wallet |
| 14 | POST | `/api/v1/wallets/{id}/adjustments` | Manual balance adjustment |
| 15 | GET | `/api/v1/wallets/{id}/ledger` | Wallet transaction audit trail |
| 16 | GET | `/api/v1/reports/costs` | Aggregated cost report (JSON or CSV) |
| 17 | GET | `/api/v1/reports/breakdown` | Per-resource cost line items (JSON or CSV) |
| 18 | GET | `/api/v1/reports/summary` | Pipeline health summary |
| 19 | GET | `/api/v1/customers/{id}/entitlements/{key}/value` | IPP-compatible balance check |
| 20 | POST | `/api/v1/reconcile` | Trigger manual OSAC reconciliation |
| 21 | GET | `/debug/dashboard` | Built-in diagnostic dashboard (HTML) |

### Metrics Server (separate port, no auth)

| Method | Path | Port | Description |
|--------|------|------|-------------|
| GET | `/metrics` | `METRICS_PORT` (default `9000`) | Prometheus metrics in text exposition format |

---

## 1. Health and Diagnostics

### GET /healthz

Kubernetes liveness probe. Returns 200 if the process is alive. No dependency checks.

**Status codes:**

| Status | Body | Meaning |
|--------|------|---------|
| `200 OK` | `{"status":"ok"}` | Process is alive |

---

### GET /readyz

Kubernetes readiness probe. Pings the PostgreSQL connection pool with a 2-second timeout.

**Status codes:**

| Status | Body | Meaning |
|--------|------|---------|
| `200 OK` | `{"status":"ready"}` | Database reachable, ready to accept traffic |
| `503 Service Unavailable` | `{"status":"not_ready","error":"database unreachable"}` | Database ping failed |

---

### GET /api/v1/debug/config

Returns the running configuration with secrets masked. Useful for verifying deployment settings.

**Response:**

```json
{
  "osac_base_url": "http://localhost:8011",
  "inventory_db_host": "postgres://****@localhost:5434/costdb",
  "reconcile_interval": "1h0m0s",
  "metering_interval": "45s",
  "rating_interval": "20s",
  "log_level": "info",
  "log_format": "text",
  "ingest_listen_addr": "localhost:8020",
  "metrics_port": "9000",
  "custom_metrics_config_path": "",
  "auth_issuer_url": "",
  "debug_dashboard": true,
  "osac_token_set": true,
  "osac_ca_cert_set": false,
  "splunk_hec_url": "",
  "splunk_token_set": false,
  "splunk_index": ""
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `osac_base_url` | string | OSAC fulfillment-service base URL |
| `inventory_db_host` | string | Database connection string with credentials masked |
| `reconcile_interval` | string | How often the reconciler runs a full List-based sync |
| `metering_interval` | string | How often the metering sweep runs |
| `rating_interval` | string | How often the rating sweep runs |
| `log_level` | string | Current log level (debug, info, warn, error) |
| `log_format` | string | Log output format (text or json) |
| `ingest_listen_addr` | string | HTTP API listen address |
| `metrics_port` | string | Prometheus metrics port |
| `custom_metrics_config_path` | string | Path to custom metrics YAML config |
| `auth_issuer_url` | string | JWT issuer URL for authentication |
| `debug_dashboard` | boolean | Whether the debug dashboard is enabled |
| `osac_token_set` | boolean | Whether an OSAC bearer token is configured |
| `osac_ca_cert_set` | boolean | Whether a custom CA certificate is configured |
| `splunk_hec_url` | string | Splunk HEC endpoint URL (empty if disabled) |
| `splunk_token_set` | boolean | Whether a Splunk HEC token is configured |
| `splunk_index` | string | Splunk index name |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Configuration returned |

---

### GET /metrics

Served on a **separate port** (default `:9000`) without authentication, following OpenShift ServiceMonitor conventions.

Returns Prometheus metrics in text exposition format. See [observability plan](observability.md) for the full metric list.

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Metrics returned |

---

## 2. Event Ingestion

### POST /api/v1/events

Ingest a CloudEvent. The event is stored immutably in `raw_events`, then processed based on its `type` field: inventory is upserted and metering entries are created.

**Supported event types:**

| Event Type | Resource | Source |
|------------|----------|--------|
| `osac.compute_instance.lifecycle` | VMaaS compute instance | OSAC metering collector |
| `osac.cluster.lifecycle` | CaaS cluster | OSAC metering collector |
| `osac.model.lifecycle` | MaaS model (legacy mock format) | MaaS simulator |
| `inference.tokens.used` | MaaS model (IPP format) | IPP external-metering plugin |
| *(custom)* | Config-driven | Custom metrics YAML |

**Request body** (CloudEvents 1.0 structured JSON):

```json
{
  "specversion": "1.0",
  "type": "inference.tokens.used",
  "source": "ipp-plugin",
  "id": "unique-event-id",
  "time": "2026-07-01T10:00:00Z",
  "subject": "tenant-acme",
  "datacontenttype": "application/json",
  "data": {
    "organization_id": "org-12345",
    "user": "alice",
    "model": "llama-3-8b",
    "prompt_tokens": 15000,
    "completion_tokens": 8000,
    "duration_ms": 1200
  }
}
```

**CloudEvents envelope fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `specversion` | string | Yes | Always `"1.0"` |
| `type` | string | Yes | Event type (determines processing path) |
| `source` | string | Yes | Event producer identifier |
| `id` | string | Yes | Unique event ID (used for deduplication) |
| `time` | ISO 8601 | Yes | Event timestamp |
| `subject` | string | No | Fallback tenant ID if not in `data` |
| `datacontenttype` | string | No | Always `"application/json"` |

**`data` payload fields (VMaaS — `osac.compute_instance.lifecycle`):**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tenant_id` | string | Yes | Tenant identifier |
| `instance_id` | string | Yes | Compute instance ID |
| `state` | string | Yes | Instance state (metered when billable) |
| `cores` | int | Yes | Number of CPU cores |
| `memory_gib` | int | Yes | Memory in GiB |
| `duration_seconds` | float | Yes | Interval length (must be positive) |
| `cpu_core_seconds` | int | Yes | CPU core-seconds consumed |
| `memory_gib_seconds` | int | Yes | Memory GiB-seconds consumed |
| `template` | string | No | VMaaS template reference |
| `catalog_item` | string | No | Catalog item name |

**`data` payload fields (CaaS — `osac.cluster.lifecycle`):**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tenant_id` | string | Yes | Tenant identifier |
| `cluster_id` | string | Yes | Cluster ID |
| `state` | string | Yes | Cluster state (metered when billable) |
| `duration_seconds` | float | Yes | Interval length (must be positive) |
| `worker_node_seconds` | int | No | Worker node-seconds consumed |
| `node_count` | int | No | Current node count |
| `template` | string | No | Cluster template |
| `host_type` | string | No | `"_control_plane"` for uptime metering |

**`data` payload fields (MaaS — `osac.model.lifecycle` or `inference.tokens.used`):**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tenant_id` | string | Conditional | Tenant ID (legacy format). Falls back to `organization_id`, subscription namespace, `group`, or `user` |
| `organization_id` | string | No | IPP organization ID (highest-priority tenant attribution) |
| `user` | string | No | IPP user identity |
| `group` | string | No | IPP group identity |
| `subscription` | string | No | IPP subscription (namespace extracted as tenant) |
| `cost_center` | string | No | IPP cost center |
| `model_id` | string | Conditional | Model deployment ID (legacy). Falls back to `model` |
| `model` | string | Conditional | Model name (IPP format) |
| `model_name` | string | No | Human-readable model name (legacy) |
| `prompt_tokens` | int | Conditional | Input tokens (IPP format). Maps to `tokens_in` |
| `completion_tokens` | int | Conditional | Output tokens (IPP format). Maps to `tokens_out` |
| `tokens_in` | int | Conditional | Input tokens (legacy format) |
| `tokens_out` | int | Conditional | Output tokens (legacy format) |
| `total_tokens` | int | No | Total tokens (informational) |
| `cached_input_tokens` | int | No | Subset of prompt tokens from cache (observability only) |
| `cache_creation_tokens` | int | No | Subset of prompt tokens for cache creation |
| `reasoning_tokens` | int | No | Subset of completion tokens for reasoning (o1/o3/DeepSeek R1) |
| `requests` | int | No | Number of inference requests (legacy) |
| `request_count` | int | No | Number of inference requests (alias for `requests`) |
| `duration_seconds` | float | No | Interval length in seconds (legacy). Falls back from `duration_ms` |
| `duration_ms` | int | No | Interval length in milliseconds (IPP format) |
| `state` | string | No | Model state (defaults to `"MODEL_STATE_RUNNING"`) |
| `template` | string | No | MaaS template reference |
| `provider` | string | No | LLM provider name |

**Status codes:**

| Status | Body | Meaning |
|--------|------|---------|
| `204 No Content` | *(empty)* | Event accepted and processed |
| `400 Bad Request` | `{"error":"..."}` | Invalid JSON, missing `id`/`type`, or missing resource/tenant IDs |
| `409 Conflict` | `{"status":"duplicate"}` | Event ID already exists (idempotent deduplication) |
| `500 Internal Server Error` | `{"error":"..."}` | Database or processing error |

**Body size limit:** 1 MB

**Processing pipeline:** On 204, the event flows through: `raw_events` (immutable store) -> inventory upsert -> `metering_entries` (3+ entries per event) -> `cost_entries` (created asynchronously by the rating sweep).

---

## 3. Rates

### GET /api/v1/rates

List all active rate cards. Supports JSON and CSV output.

**Query parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `tenant_id` | string | No | Filter rates by tenant ID. Omit for global rates |
| `format` | string | No | `"csv"` for CSV download. Also triggered by `Accept: text/csv` header |

**Response (JSON):**

```json
{
  "rates": [
    {
      "id": 1,
      "tenant_id": null,
      "resource_type": "Model",
      "instance_type": "",
      "meter_name": "maas_tokens_in",
      "koku_metric": "cpu_core_request_per_hour",
      "cost_type": "Infrastructure",
      "price_per_unit": "0.000003",
      "currency": "USD",
      "tiers": [
        {"up_to": 1000000, "price_per_unit": "0.000003"},
        {"up_to": null, "price_per_unit": "0.0000025"}
      ],
      "tier_mode": "volume",
      "tier_period": "monthly",
      "description": "Input tokens for LLM inference",
      "effective_from": "2026-01-01T00:00:00Z",
      "effective_to": null
    }
  ],
  "count": 1
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `rates` | array | List of rate records |
| `rates[].id` | int | Rate record ID |
| `rates[].tenant_id` | string or null | Tenant-specific override (null = global default) |
| `rates[].resource_type` | string | Resource type (e.g., `ComputeInstance`, `Cluster`, `Model`) |
| `rates[].instance_type` | string | Instance type filter (empty = all) |
| `rates[].meter_name` | string | Meter this rate applies to |
| `rates[].koku_metric` | string | Koku-compatible metric name |
| `rates[].cost_type` | string | `Infrastructure` or `Supplementary` (Koku cost layer) |
| `rates[].price_per_unit` | decimal | Price per unit of metered value |
| `rates[].currency` | string | Currency code (e.g., `USD`) |
| `rates[].tiers` | array | Tiered pricing tiers (empty = flat rate) |
| `rates[].tiers[].up_to` | float or null | Upper bound for this tier (null = unlimited) |
| `rates[].tiers[].price_per_unit` | decimal | Price per unit within this tier |
| `rates[].tier_mode` | string | Tier calculation mode (`volume` or `graduated`) |
| `rates[].tier_period` | string | Period for tier accumulation (e.g., `monthly`) |
| `rates[].description` | string | Human-readable description |
| `rates[].effective_from` | ISO 8601 | Rate effective start date |
| `rates[].effective_to` | ISO 8601 or null | Rate effective end date (null = no expiry) |
| `count` | int | Number of rates returned |

**CSV output columns:** `id,tenant_id,resource_type,instance_type,meter_name,cost_type,price_per_unit,currency,tier_mode,tier_period,tiers,description,effective_from,effective_to`

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Rates returned |
| `500 Internal Server Error` | Database query failed |

---

## 4. Quotas

### POST /api/v1/quotas

Create a usage or budget quota for a tenant or project.

**Request body:**

```json
{
  "tenant_id": "tenant-acme",
  "project_id": "",
  "resource_type": "Model",
  "meter_name": "maas_tokens_in",
  "limit_value": 5000000,
  "unit": "tokens",
  "period": "monthly",
  "policy": "deny",
  "thresholds": [50, 70, 90, 100],
  "effective_from": "2026-07-01T00:00:00Z"
}
```

**Request body fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tenant_id` | string | Yes | Tenant identifier |
| `project_id` | string | No | Project scope (empty = tenant-wide) |
| `resource_type` | string | No | Resource type filter |
| `meter_name` | string | Yes | Meter to limit. `"*"` for budget quotas covering all meters |
| `limit_value` | float | Yes | Maximum allowed value (must be positive) |
| `unit` | string | Yes | Unit of measurement. Currency codes (USD, EUR, etc.) create budget quotas |
| `period` | string | No | Quota period: `monthly`, `daily`, `Nh` (N hours), `Nd` (N days). Default: `monthly` |
| `policy` | string | No | Enforcement policy: `deny` or `warn`. Default: `deny` |
| `thresholds` | float[] | No | Alert threshold percentages. Default: `[50, 70, 90, 100]` |
| `effective_from` | ISO 8601 | No | When the quota takes effect. Default: now |
| `effective_to` | ISO 8601 | No | When the quota expires (null = no expiry) |

**Status codes:**

| Status | Body | Meaning |
|--------|------|---------|
| `201 Created` | QuotaRecord JSON | Quota created successfully |
| `400 Bad Request` | `{"error":"..."}` | Missing required fields, invalid period, or project overcommit |
| `500 Internal Server Error` | `{"error":"..."}` | Database error |

---

### GET /api/v1/quotas

List all active quotas, optionally filtered by tenant. Supports inline status enrichment.

**Query parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `tenant_id` | string | No | Filter by tenant ID |
| `status` | string | No | Set to `"true"` to include consumption status, percentage, and thresholds |

**Response (without status):**

```json
{
  "quotas": [
    {
      "id": 1,
      "name": "",
      "tenant_id": "tenant-acme",
      "project_id": "",
      "resource_type": "Model",
      "meter_name": "maas_tokens_in",
      "limit_value": 5000000,
      "unit": "tokens",
      "period": "monthly",
      "policy": "deny",
      "thresholds": [50, 70, 90, 100],
      "effective_from": "2026-07-01T00:00:00Z",
      "effective_to": null
    }
  ]
}
```

**Response (with `?status=true`):**

```json
{
  "quotas": [
    {
      "id": 1,
      "tenant_id": "tenant-acme",
      "meter_name": "maas_tokens_in",
      "limit_value": 5000000,
      "unit": "tokens",
      "period": "monthly",
      "policy": "deny",
      "consumed": 3200000,
      "percentage": 64.0,
      "thresholds": {"50": true, "70": false, "90": false, "100": false}
    }
  ]
}
```

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Quotas returned |
| `500 Internal Server Error` | Database query failed |

---

### PUT /api/v1/quotas/{id}

Update an existing quota. Only provided fields are changed.

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | int | Yes | Quota record ID |

**Request body:** Same fields as POST (all optional for update).

```json
{
  "limit_value": 10000000,
  "thresholds": [25, 50, 75, 100]
}
```

**Status codes:**

| Status | Body | Meaning |
|--------|------|---------|
| `200 OK` | Updated QuotaRecord JSON | Quota updated |
| `400 Bad Request` | `{"error":"..."}` | Invalid quota ID, invalid period, or project overcommit |
| `404 Not Found` | `{"error":"..."}` | Quota not found |

---

### DELETE /api/v1/quotas/{id}

Soft-delete a quota. Sets `effective_to` to now rather than removing the record.

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | int | Yes | Quota record ID |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `204 No Content` | Quota soft-deleted |
| `400 Bad Request` | Invalid quota ID |
| `404 Not Found` | Quota not found |

---

### GET /api/v1/quotas/{tenant_id}

Returns real-time quota consumption status for a tenant, including threshold checks and fired alerts. Supports both tenant-level and project-level quotas.

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `tenant_id` | string | Yes | Tenant identifier |

**Response:**

```json
{
  "tenant_id": "tenant-acme",
  "period": "2026-07",
  "quotas": [
    {
      "meter_name": "maas_tokens_in",
      "unit": "tokens",
      "limit": 5000000,
      "consumed": 4450000,
      "percentage": 89.0,
      "thresholds": {"50": true, "70": true, "90": false, "100": false},
      "alerts": [
        {
          "id": 1,
          "tenant_id": "tenant-acme",
          "meter_name": "maas_tokens_in",
          "threshold_pct": 70,
          "consumed": 3500000,
          "limit_value": 5000000,
          "period": "2026-07",
          "state": "firing",
          "fired_at": "2026-07-15T08:30:00Z"
        }
      ]
    }
  ],
  "projects": {
    "project-alpha": [
      {
        "meter_name": "maas_tokens_in",
        "unit": "tokens",
        "limit": 2000000,
        "consumed": 1200000,
        "percentage": 60.0,
        "thresholds": {"50": true, "70": false, "90": false, "100": false}
      }
    ]
  }
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `tenant_id` | string | Tenant identifier |
| `period` | string | Current billing period label (e.g., `2026-07`) |
| `quotas` | array | Tenant-level quota statuses |
| `quotas[].meter_name` | string | Meter this quota applies to |
| `quotas[].unit` | string | Unit of measurement |
| `quotas[].limit` | float | Quota limit for the current period |
| `quotas[].consumed` | float | Current consumption (from `metering_entries` or `cost_entries` for budget quotas) |
| `quotas[].percentage` | float | `consumed / limit * 100`, rounded to 2 decimal places |
| `quotas[].thresholds` | object | Map of threshold percentage to whether it has been reached |
| `quotas[].alerts` | array | Threshold alerts fired for this meter in this period (omitted if none) |
| `projects` | object | Map of project ID to project-level quota statuses (omitted if none) |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Quota status returned |
| `400 Bad Request` | Missing or invalid tenant_id |
| `500 Internal Server Error` | Database query failed |

**Notes:**
- Consumption is computed in real-time from `metering_entries` (usage quotas) or `cost_entries` (budget quotas where unit is a currency code).
- Budget quotas with `meter_name="*"` report total cost across all meters.
- Threshold levels default to `[50, 70, 90, 100]` if not configured on the quota.

---

## 5. Wallets

### POST /api/v1/wallets

Create a prepaid wallet for a tenant or project.

**Request body:**

```json
{
  "tenant_id": "tenant-acme",
  "project_id": "",
  "currency": "USD",
  "thresholds": [50, 25, 10, 0]
}
```

**Request body fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tenant_id` | string | Yes | Tenant identifier |
| `project_id` | string | No | Project scope (empty = tenant-wide) |
| `currency` | string | No | Currency code. Default: `USD` |
| `thresholds` | float[] | No | Remaining-balance alert thresholds (percentages). Default: `[50, 25, 10, 0]` |

**Response:** `201 Created` with the created `WalletRecord`.

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "tenant_id": "tenant-acme",
  "project_id": "",
  "currency": "USD",
  "balance": "0",
  "balance_floor": "0",
  "reference_balance": "0",
  "lifecycle_state": "active",
  "thresholds": [50, 25, 10, 0],
  "created_at": "2026-07-01T00:00:00Z",
  "updated_at": "2026-07-01T00:00:00Z"
}
```

**Status codes:**

| Status | Meaning |
|--------|---------|
| `201 Created` | Wallet created |
| `400 Bad Request` | Missing tenant_id or invalid JSON |
| `500 Internal Server Error` | Database error |

---

### GET /api/v1/wallets/{id}

Get wallet balance and status. The `{id}` can be either a wallet UUID or a tenant ID (looks up the tenant's wallet).

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | string | Yes | Wallet UUID or tenant ID |

**Response:**

```json
{
  "wallet_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "tenant_id": "tenant-acme",
  "currency": "USD",
  "balance": "750.00",
  "reference_balance": "1000.00",
  "remaining_pct": 75.0,
  "balance_floor": "0",
  "balance_status": "ok",
  "within_balance": true,
  "thresholds": {"50": false, "25": false, "10": false, "0": false}
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `wallet_id` | string | Wallet UUID |
| `tenant_id` | string | Owning tenant |
| `currency` | string | Currency code |
| `balance` | decimal | Current balance |
| `reference_balance` | decimal | Total funds ever added (baseline for percentage calculation) |
| `remaining_pct` | float | `balance / reference_balance * 100`, rounded to 2 decimal places |
| `balance_floor` | decimal | Minimum balance threshold (below = depleted) |
| `balance_status` | string | `"ok"` or `"depleted"` |
| `within_balance` | boolean | `true` if balance > balance_floor |
| `thresholds` | object | Map of threshold percentage to whether remaining_pct is at or below that level |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Wallet status returned |
| `400 Bad Request` | Missing wallet/tenant ID |
| `404 Not Found` | Wallet not found |

---

### POST /api/v1/wallets/{id}/top-ups

Add funds to a wallet.

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | string | Yes | Wallet UUID |

**Request body:**

```json
{
  "amount": "500.00",
  "currency": "USD",
  "external_ref": "PO-2026-0042"
}
```

**Request body fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `amount` | decimal | Yes | Amount to add (must be positive) |
| `currency` | string | No | Currency code |
| `external_ref` | string | No | External reference (e.g., purchase order number) |

**Response:** `201 Created` with the created `WalletLedgerEntry`.

```json
{
  "id": 1,
  "wallet_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "entry_type": "top_up",
  "amount": "500.00",
  "balance_after": "1500.00",
  "currency": "USD",
  "external_ref": "PO-2026-0042",
  "created_at": "2026-07-15T10:00:00Z"
}
```

**Status codes:**

| Status | Meaning |
|--------|---------|
| `201 Created` | Top-up applied |
| `400 Bad Request` | Invalid amount (zero or negative) or wallet not found |

---

### POST /api/v1/wallets/{id}/adjustments

Apply a manual balance adjustment to a wallet.

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | string | Yes | Wallet UUID |

**Request body:**

```json
{
  "amount": "100.00",
  "reason": "Billing correction"
}
```

**Request body fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `amount` | decimal | Yes | Adjustment amount (must be non-zero; positive adds funds, negative not yet implemented) |
| `external_ref` | string | No | External reference |
| `reason` | string | No | Reason for the adjustment |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `201 Created` | Positive adjustment applied |
| `400 Bad Request` | Amount is zero |
| `501 Not Implemented` | Negative adjustments not yet supported |

---

### GET /api/v1/wallets/{id}/ledger

Retrieve the transaction audit trail for a wallet.

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | string | Yes | Wallet UUID |

**Query parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `limit` | int | No | Maximum number of entries to return. Default: `100` |

**Response:**

```json
{
  "entries": [
    {
      "id": 1,
      "wallet_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "entry_type": "top_up",
      "amount": "1000.00",
      "balance_after": "1000.00",
      "currency": "USD",
      "external_ref": "PO-2026-0001",
      "created_at": "2026-07-01T00:00:00Z"
    },
    {
      "id": 2,
      "wallet_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "entry_type": "charge",
      "amount": "-25.50",
      "balance_after": "974.50",
      "currency": "USD",
      "cost_entry_id": 42,
      "created_at": "2026-07-02T12:00:00Z"
    }
  ]
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `entries` | array | Ledger entries in chronological order |
| `entries[].id` | int | Ledger entry ID |
| `entries[].wallet_id` | string | Wallet UUID |
| `entries[].entry_type` | string | Entry type: `top_up`, `charge`, `adjustment` |
| `entries[].amount` | decimal | Transaction amount (positive for credits, negative for charges) |
| `entries[].balance_after` | decimal | Wallet balance after this transaction |
| `entries[].currency` | string | Currency code |
| `entries[].cost_entry_id` | int or null | Linked cost entry ID (for charges) |
| `entries[].external_ref` | string | External reference (for top-ups/adjustments) |
| `entries[].reason` | string | Reason (for adjustments) |
| `entries[].created_at` | ISO 8601 | Timestamp |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Ledger returned |
| `500 Internal Server Error` | Database query failed |

---

## 6. Reports

### GET /api/v1/reports/costs

Aggregated cost report with Koku-compatible response structure. Supports grouping, daily resolution, and CSV export.

**Query parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `tenant_id` | string | No | Filter by tenant ID |
| `resource_type` | string | No | Filter by resource type (e.g., `Model`, `ComputeInstance`, `Cluster`) |
| `group_by` | string | No | Grouping dimension: `tenant`, `type`, `meter`, `resource`, `project`, `user`. Default: `tenant` |
| `resolution` | string | No | Time resolution: `daily` for day-by-day breakdown. Omit for period total |
| `period` | string | No | Period in `YYYY-MM` format. Default: current month. Ignored if `from` is set |
| `from` | string | No | Start date (`YYYY-MM-DD` or RFC 3339). Overrides `period` |
| `to` | string | No | End date (`YYYY-MM-DD` or RFC 3339). Default: now (only used with `from`) |
| `format` | string | No | `"csv"` for CSV download. Also triggered by `Accept: text/csv` header |

**Response (JSON):**

```json
{
  "meta": {
    "total": {
      "cost": {
        "usage": {"value": 125.50, "units": "USD"},
        "total": {"value": 125.50, "units": "USD"},
        "raw": {"value": 0, "units": "USD"},
        "markup": {"value": 0, "units": "USD"}
      },
      "infrastructure": {
        "usage": {"value": 100.00, "units": "USD"},
        "total": {"value": 100.00, "units": "USD"},
        "raw": {"value": 0, "units": "USD"},
        "markup": {"value": 0, "units": "USD"}
      },
      "supplementary": {
        "usage": {"value": 25.50, "units": "USD"},
        "total": {"value": 25.50, "units": "USD"},
        "raw": {"value": 0, "units": "USD"},
        "markup": {"value": 0, "units": "USD"}
      },
      "cost_units": "USD"
    },
    "period": "2026-07",
    "group_by": "tenant",
    "resolution": "",
    "filters": {"tenant_id": "tenant-acme"}
  },
  "data": [
    {
      "group": "tenant-acme",
      "entries": 1500,
      "cost": 125.50,
      "infrastructure_cost": 100.00,
      "supplementary_cost": 25.50,
      "currency": "USD"
    }
  ]
}
```

**Data row fields:**

| Field | Type | Description |
|-------|------|-------------|
| `date` | string | Date string (only present with `resolution=daily`) |
| `group` | string | Group key (tenant ID, resource type, meter name, etc.) |
| `entries` | int | Number of cost entries in this group |
| `cost` | float | Total cost (infrastructure + supplementary) |
| `infrastructure_cost` | float | Infrastructure cost (Koku cost layer) |
| `supplementary_cost` | float | Supplementary cost (Koku cost layer) |
| `currency` | string | Currency code |

**CSV output columns:** `group,entries,cost,infrastructure_cost,supplementary_cost,currency` (with `date` prepended when `resolution=daily`).

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Report returned |
| `400 Bad Request` | Invalid period, from, or to format |
| `500 Internal Server Error` | Database query failed |

---

### GET /api/v1/reports/breakdown

Per-resource cost line items showing individual metering and cost entries. Supports CSV export.

**Query parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `tenant_id` | string | No | Filter by tenant ID |
| `resource_type` | string | No | Filter by resource type |
| `from` | string | No | Start date (`YYYY-MM-DD` or RFC 3339). Default: 1st of current month |
| `to` | string | No | End date (`YYYY-MM-DD` or RFC 3339). Default: now |
| `limit` | int | No | Maximum rows to return. Default: `100` |
| `format` | string | No | `"csv"` for CSV download. Also triggered by `Accept: text/csv` header |

**Response (JSON):**

```json
{
  "meta": {
    "count": 3,
    "filters": {"tenant_id": "tenant-acme"}
  },
  "data": [
    {
      "date": "2026-07-15",
      "tenant_id": "tenant-acme",
      "project_id": "project-alpha",
      "user_id": "alice",
      "resource_type": "Model",
      "resource_id": "llama-3-8b",
      "meter_name": "maas_tokens_in",
      "metered_value": 15000,
      "cost_amount": 0.045,
      "cost_type": "Infrastructure",
      "currency": "USD"
    }
  ]
}
```

**Data row fields:**

| Field | Type | Description |
|-------|------|-------------|
| `date` | string | Date of the cost entry |
| `tenant_id` | string | Tenant identifier |
| `project_id` | string | Project identifier (empty if not set) |
| `user_id` | string | User identifier (empty if not set) |
| `resource_type` | string | Resource type (e.g., `Model`, `ComputeInstance`) |
| `resource_id` | string | Resource identifier |
| `meter_name` | string | Meter name (e.g., `maas_tokens_in`) |
| `metered_value` | float | Raw metered value |
| `cost_amount` | float | Calculated cost amount |
| `cost_type` | string | `Infrastructure` or `Supplementary` |
| `currency` | string | Currency code |

**CSV output columns:** `date,tenant_id,project_id,user_id,resource_type,resource_id,meter_name,metered_value,cost_amount,cost_type,currency`

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Breakdown returned |
| `400 Bad Request` | Invalid from or to format |
| `500 Internal Server Error` | Database query failed |

---

### GET /api/v1/reports/summary

Pipeline health summary showing record counts across all tables. Used for operational monitoring.

**Response:**

```json
{
  "raw_events": 12500,
  "metering_entries": 37500,
  "cost_entries": 37500,
  "rates": 12,
  "live_vms": 45,
  "live_clusters": 3,
  "live_models": 8
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `raw_events` | int | Total raw events stored |
| `metering_entries` | int | Total metering entries |
| `cost_entries` | int | Total cost entries |
| `rates` | int | Number of active rate records |
| `live_vms` | int | Active compute instances (non-deleted) |
| `live_clusters` | int | Active clusters (non-deleted) |
| `live_models` | int | Active model deployments (non-deleted) |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Summary returned |
| `500 Internal Server Error` | Database query failed |

---

## 7. IPP Compatibility

### GET /api/v1/customers/{id}/entitlements/{key}/value

IPP-compatible balance check endpoint. Returns an entitlement value response matching the format expected by the IPP external-metering plugin.

Source: [IPP external-metering client](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/main/pkg/plugins/external-metering/client.go)

**Path parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | string | Yes | Customer (tenant) ID |
| `key` | string | Yes | Feature/entitlement key (accepted but not currently used for filtering) |

**Query parameters:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `model` | string | No | Model name (reserved for future feature-scoped quotas) |

**Response:**

```json
{
  "hasAccess": true,
  "balance": 550000,
  "usage": 4450000,
  "overage": 0
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `hasAccess` | boolean | `true` if total usage is below total quota limit |
| `balance` | float | Remaining quota balance (limit - usage, floored at 0) |
| `usage` | float | Total consumption across all quotas for this tenant |
| `overage` | float | Amount exceeding quota limit (0 if within limits) |

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Entitlement value returned |
| `400 Bad Request` | Invalid path format |

**Notes:**
- If the tenant has no quotas defined, returns `hasAccess: true` with `balance` set to the maximum float64 value (effectively unlimited).
- Aggregates across all quotas for the tenant within their respective periods.

---

## 8. Operations

### POST /api/v1/reconcile

Trigger a manual OSAC reconciliation. The reconciler performs a full List-based sync against the OSAC fulfillment-service to correct any drift in inventory state.

**Request body:** None.

**Response:**

```json
{"status": "reconciliation triggered"}
```

**Status codes:**

| Status | Body | Meaning |
|--------|------|---------|
| `202 Accepted` | `{"status":"reconciliation triggered"}` | Reconciliation started in background |
| `429 Too Many Requests` | `{"error":"reconciliation already in progress"}` | A reconciliation is already running |
| `503 Service Unavailable` | `{"error":"reconciler not configured"}` | Reconciler component is not initialized |

**Notes:**
- The reconciliation runs asynchronously in a goroutine.
- Only one reconciliation can run at a time (enforced via atomic flag).
- The OSAC watcher and reconciler must be enabled (not in `DISABLE_COMPONENTS`).

---

### GET /debug/dashboard

Built-in diagnostic dashboard rendered as a single-page HTML application. Provides a live view of inventory, metering, costs, quotas, and wallet status.

**Availability:** Only registered when `DEBUG_DASHBOARD=true` (default: enabled). When enabled, `GET /` redirects to `/debug/dashboard`.

**Response:** `200 OK` with `Content-Type: text/html; charset=utf-8`.

**Status codes:**

| Status | Meaning |
|--------|---------|
| `200 OK` | Dashboard HTML returned |
| `404 Not Found` | Dashboard is disabled (`DEBUG_DASHBOARD=false`) |

---

## Common Patterns

### Error Response Format

All error responses use a consistent JSON format:

```json
{"error": "descriptive error message"}
```

### CSV Export

Endpoints that support CSV export (`/api/v1/rates`, `/api/v1/reports/costs`, `/api/v1/reports/breakdown`) can be triggered two ways:

1. Query parameter: `?format=csv`
2. Accept header: `Accept: text/csv`

CSV responses include a `Content-Disposition: attachment` header with a suggested filename.

### CORS

All data endpoints include `Access-Control-Allow-Origin: *` headers.

### Authentication

All endpoints on the main API server pass through JWT authentication middleware when `AUTH_ISSUER_URL` is configured. The `/healthz` and `/readyz` probes are exempt from authentication (required for Kubernetes probe access without tokens).

### Body Size Limits

All POST/PUT request bodies are limited to 1 MB. ID fields (resource_id, tenant_id) are limited to 256 characters.

---

## OSAC Data Sources (non-HTTP)

The cost-event-consumer also consumes data from the OSAC fulfillment-service via Watch stream and List APIs. These are not HTTP endpoints exposed by this service, but upstream data sources:

| Source | Path | Description |
|--------|------|-------------|
| OSAC REST/gRPC | `Watch` stream | Real-time SSE/gRPC event stream |
| OSAC REST | `GET /api/fulfillment/v1/compute_instances` | List VMs (reconciliation) |
| OSAC REST | `GET /api/fulfillment/v1/clusters` | List clusters (reconciliation) |
| OSAC REST | `GET /api/fulfillment/v1/instance_types` | List instance types |
| OSAC REST | `GET /api/fulfillment/v1/projects` | List projects |

See [gRPC Messages Catalog](grpc-messages-catalog.md) for message definitions.
