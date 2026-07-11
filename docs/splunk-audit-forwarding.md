# Splunk Audit Log Forwarding

The cost-event-consumer includes an optional Splunk HEC (HTTP Event
Collector) forwarder that streams the immutable `raw_events` audit log
to Splunk for compliance, search, and dispute resolution.

## Architecture

```
raw_events (PostgreSQL)
    │
    │  poll every SPLUNK_INTERVAL (default 10s)
    │  cursor-based: splunk_cursor.last_sent_id
    ▼
┌──────────────────┐
│ Splunk Forwarder  │  internal/splunk/forwarder.go
│  (in-process)     │  batches of 100, newline-delimited JSON
└────────┬─────────┘
         │ POST /services/collector/event
         │ Authorization: Splunk <token>
         ▼
┌──────────────────┐
│   Splunk HEC      │  port 8088 (TLS)
│                   │
│  index=main       │  sourcetype=_json
│  source=cost-     │  host=cost-event-consumer
│   event-consumer  │
└──────────────────┘
```

## Delivery Semantics

- **At-least-once.** The cursor advances only after Splunk acknowledges
  each batch (HTTP 200/201). On failure the batch is retried.
- **Cursor table:** `splunk_cursor` — single-row table tracking
  `last_sent_id` (the BIGSERIAL id from `raw_events`).
- **Batch size:** 100 events per HEC POST.
- **Backpressure:** 3 consecutive HEC errors abandon the current sweep;
  the next sweep retries from the same cursor position.

## Configuration

All configuration is via environment variables. The forwarder is **opt-in** —
it only starts when `SPLUNK_HEC_URL` is set.

| Variable | Required | Default | Description |
|---|---|---|---|
| `SPLUNK_HEC_URL` | Yes | — | Full HEC endpoint URL, e.g. `https://splunk:8088/services/collector/event` |
| `SPLUNK_HEC_TOKEN` | Yes | — | HEC authentication token |
| `SPLUNK_INDEX` | No | (Splunk default) | Target Splunk index name |
| `SPLUNK_INTERVAL` | No | `10s` | Polling interval (Go duration, e.g. `5s`, `30s`) |
| `SPLUNK_TLS_INSECURE` | No | `false` | Skip TLS certificate verification (PoC/dev only) |

## Event Format

Each HEC event wraps a `raw_events` row:

```json
{
  "time": 1783684800.5,
  "host": "cost-event-consumer",
  "source": "cost-event-consumer",
  "sourcetype": "_json",
  "index": "main",
  "event": {
    "id": 42,
    "event_id": "019f38b0-6e69-7ed8-9d07-dfc5cbd85d6e",
    "event_type": "EVENT_TYPE_OBJECT_CREATED",
    "event_source": "osac.fulfillment-service",
    "event_time": "2026-07-10T12:00:00Z",
    "tenant_id": "shared",
    "resource_type": "ComputeInstance",
    "resource_id": "019f38b0-6e67-740f-83ea-0428f9880349",
    "data": { "...full CloudEvent or OSAC event payload..." },
    "received_at": "2026-07-10T12:00:01Z"
  }
}
```

Searchable fields in Splunk: `event_type`, `event_source`, `tenant_id`,
`resource_type`, `resource_id`, plus any nested fields in `data`.

## Prometheus Metrics

| Metric | Type | Description |
|---|---|---|
| `cost_consumer_splunk_forward_total` | Counter | Raw events forwarded to Splunk |
| `cost_consumer_splunk_forward_errors_total` | Counter | HEC post failures |
| `cost_consumer_splunk_forward_duration_seconds` | Histogram | Sweep duration |

## Database Schema

```sql
CREATE TABLE IF NOT EXISTS splunk_cursor (
    id             INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_sent_id   BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Single-row table — the `CHECK (id = 1)` constraint ensures exactly one
cursor row. Auto-created on startup via the schema migration in `store.go`.

## Deployment

### Local / Docker Compose

Start a Splunk container alongside the consumer:

```bash
docker run -d --name splunk \
  -p 18000:8000 -p 18088:8088 \
  -e SPLUNK_START_ARGS="--accept-license" \
  -e SPLUNK_PASSWORD="changeme123" \
  -e SPLUNK_HEC_TOKEN="cost-audit-token" \
  splunk/splunk:9.4
```

Then start the consumer with:

```bash
SPLUNK_HEC_URL="https://localhost:18088/services/collector/event" \
SPLUNK_HEC_TOKEN="cost-audit-token" \
SPLUNK_TLS_INSECURE="true" \
SPLUNK_INTERVAL="5s" \
./inventory-watcher
```

### Kubernetes / OpenShift

Deploy manifests from `deploy/k8s/splunk/`:

```bash
bash deploy/k8s/splunk/deploy.sh
```

This deploys a single-replica Splunk instance in the `cost-mgmt` namespace
and patches the `cost-event-consumer` deployment with the Splunk env vars.

Verify with:

```bash
bash deploy/k8s/splunk/test.sh
```

### Splunk Search

After forwarding starts, search in Splunk with:

```
index=main sourcetype=_json source=cost-event-consumer
```

Filter by tenant or resource type:

```
index=main sourcetype=_json source=cost-event-consumer tenant_id="shared" resource_type="ComputeInstance"
```

## Source Files

| File | Purpose |
|---|---|
| [`internal/splunk/forwarder.go`](../inventory-watcher/internal/splunk/forwarder.go) | HEC forwarder implementation |
| [`internal/splunk/forwarder_test.go`](../inventory-watcher/internal/splunk/forwarder_test.go) | Unit tests |
| [`internal/inventory/store.go`](../inventory-watcher/internal/inventory/store.go) | `splunk_cursor` schema, `SplunkCursor()`, `AdvanceSplunkCursor()`, `RawEventsSince()` |
| [`internal/config/config.go`](../inventory-watcher/internal/config/config.go) | `SPLUNK_*` environment variables |
| [`internal/metrics/metrics.go`](../inventory-watcher/internal/metrics/metrics.go) | Prometheus metric definitions |
| [`cmd/consumer/main.go`](../inventory-watcher/cmd/consumer/main.go) | Forwarder wiring (opt-in on `SPLUNK_HEC_URL`) |
| [`deploy/k8s/splunk/`](../deploy/k8s/splunk/) | Kubernetes manifests + deploy/test scripts |
