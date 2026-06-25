# Demo Scenario 2: MaaS Metering End-to-End

## Purpose

Demonstrate consumption-based metering for Model-as-a-Service (MaaS).
Show that the metering pipeline handles token/request events at speed,
tracks multiple models and tenants, and produces correct per-tenant
aggregations.

## Prerequisites

Same as demo scenario 1, plus build the simulator:

```bash
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
go build -o maas-simulator ./cmd/maas-simulator/
```

## Demo Flow

### Act 1: Start the consumer with the ingest endpoint

**Terminal 1 — consumer logs:**
```bash
cd inventory-watcher
OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
INGEST_LISTEN_ADDR=localhost:8020 \
./inventory-watcher
```

Point out: `ingest endpoint listening addr=localhost:8020` — this is the
HTTP endpoint that accepts MaaS CloudEvents for testing.

### Act 2: Show the empty state

**Terminal 2 — live model inventory:**
```bash
watch -n 2 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT model_id, model_name, tenant, state FROM inventory_model ORDER BY model_name;"'
```

Table is empty — no models yet.

### Act 3: Fire MaaS events

**Terminal 3 — run the simulator:**

Start small, show it working:
```bash
./maas-simulator -target http://localhost:8020 -count 10 -rate 5
```

Then crank it up:
```bash
./maas-simulator -target http://localhost:8020 -count 500 -rate 200
```

**What to show:**
- Terminal 1 (logs): rapid `stored raw event ... type=osac.model.lifecycle`
  and `metered MaaS event` messages
- Terminal 2 (watch): 4 models appear across 3 tenants
- Terminal 3 (simulator): throughput counter updating in real-time

### Act 4: Show metering entries

**Terminal 2 — switch to metering view:**
```bash
watch -n 2 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, count(*) as entries, round(sum(value)::numeric, 0) as total, unit
   FROM metering_entries WHERE resource_type = '"'"'model'"'"'
   GROUP BY meter_name, unit ORDER BY meter_name;"'
```

Shows 4 meter types accumulating: `maas_tokens_in`, `maas_tokens_out`,
`maas_inference_tokens`, `maas_requests`.

### Act 5: Per-tenant breakdown

**Terminal 2:**
```bash
watch -n 2 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT tenant_id, meter_name, round(sum(value)::numeric, 0) as total
   FROM metering_entries WHERE resource_type = '"'"'model'"'"'
   GROUP BY tenant_id, meter_name ORDER BY tenant_id, meter_name;"'
```

Shows consumption broken down by tenant — each tenant uses different amounts
based on their inference load.

### Act 6: Per-model breakdown

**Terminal 2:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT m.model_name, me.meter_name, round(sum(me.value)::numeric, 0) as total
   FROM metering_entries me
   JOIN inventory_model m ON me.resource_id = m.model_id
   WHERE me.resource_type = 'model'
   GROUP BY m.model_name, me.meter_name
   ORDER BY m.model_name, me.meter_name;"
```

Shows which models are consuming the most tokens — useful for cost
allocation per model type (llama-3-8b vs llama-3-70b pricing).

### Act 7: Show the raw event log

```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT count(*) as total_events,
          min(received_at) as first_event,
          max(received_at) as last_event,
          max(received_at) - min(received_at) as duration
   FROM raw_events WHERE resource_type = 'Model';"
```

Shows N events processed in M seconds — all stored immutably.

## Simulator Options

```
Usage: maas-simulator [flags]
  -target string   ingest endpoint URL (default "http://localhost:8020")
  -count int       total events to send (default 100)
  -rate int        events per second, 0=unlimited (default 50)
  -workers int     concurrent senders (default 4)
```

Examples:
```bash
# Quick smoke test
./maas-simulator -count 10 -rate 5

# Sustained load
./maas-simulator -count 1000 -rate 100

# Burst test (as fast as possible)
./maas-simulator -count 1000 -rate 0 -workers 8

# Long-running test
./maas-simulator -count 10000 -rate 50
```

## Talking Points

1. **Consumption-based vs capacity-based** — MaaS meters token counts and
   requests, not provisioned resources × time. You pay for what you use.

2. **Event-driven metering** — each MaaS event produces metering entries
   immediately on arrival. No periodic sweep needed (unlike VM metering).

3. **Multi-tenant isolation** — each tenant's consumption is tracked
   independently. Rate structure can vary per tenant (req #6).

4. **Multi-model tracking** — different models can have different per-token
   rates (llama-3-8b cheaper than llama-3-70b).

5. **Same pipeline** — MaaS events flow through the same raw_events →
   inventory → metering_entries pipeline as VM events. No separate system.

6. **Throughput** — pipeline handles ~100 events/second on a laptop. In
   production with connection pooling and batching, significantly more.

7. **OSAC readiness** — OSAC doesn't emit model events yet. When it does,
   we add a Model case to the Watch stream dispatcher. The ingest endpoint
   is for testing; in production, events come from the Watch stream.
