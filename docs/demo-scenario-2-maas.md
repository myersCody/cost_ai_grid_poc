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
`maas_requests`.

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

### Act 7: Cost breakdown (the punchline)

**Goal:** Show that metering entries have been automatically rated and
converted to dollar amounts.

Wait ~35 seconds after firing events for the rating sweep to run (30s interval).

**Terminal 2 — total cost by tenant:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT tenant_id, round(sum(cost_amount)::numeric, 4) as total_cost, currency
   FROM cost_entries GROUP BY tenant_id, currency ORDER BY total_cost DESC;"
```

**Terminal 2 — cost by meter type:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, count(*) as entries,
          round(sum(cost_amount)::numeric, 6) as total_cost, currency
   FROM cost_entries WHERE resource_type = 'model'
   GROUP BY meter_name, currency ORDER BY total_cost DESC;"
```

**Terminal 2 — cost per model per tenant:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT m.model_name, ce.tenant_id,
          round(sum(ce.cost_amount)::numeric, 4) as cost, ce.currency
   FROM cost_entries ce
   JOIN inventory_model m ON ce.resource_id = m.model_id
   GROUP BY m.model_name, ce.tenant_id, ce.currency
   ORDER BY m.model_name, ce.tenant_id;"
```

**What to explain:**
- "tenant-acme consumed 1.8M input tokens × $0.50/M = $0.90 in token_in cost"
- Output tokens cost 3× more than input tokens ($1.50 vs $0.50 per million)
- Rates are configurable per tenant — a premium tenant could pay different rates

**Terminal 2 — show the rate definitions:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT resource_type, meter_name, price_per_unit, currency FROM rates
   WHERE resource_type = 'model' ORDER BY meter_name;"
```

Or run the full cost report:
```bash
bash snippets/query-costs.sh
```

### Act 8: Show the raw event log

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

1. **Full pipeline: events → metering → cost** — a MaaS event arrives,
   metering entries are produced immediately, and the rating sweep converts
   them to dollar amounts within 30 seconds. End-to-end.

2. **Consumption-based vs capacity-based** — MaaS meters token counts and
   requests, not provisioned resources × time. You pay for what you use.

3. **Configurable rates** — default rates seeded on startup ($0.50/M input
   tokens, $1.50/M output tokens, etc.). Rates can be overridden per tenant
   for custom pricing. Tiered pricing supported (first N units free, etc.).

4. **Multi-tenant cost isolation** — each tenant's consumption and cost are
   tracked independently. The cost-by-tenant query is the billing view.

5. **Multi-model tracking** — different models can have different per-token
   rates. llama-3-70b should cost more than llama-3-8b — just add a rate
   override for the specific model's meter.

6. **Same pipeline for VMs and models** — MaaS events flow through the same
   raw_events → inventory → metering → cost pipeline as VM events. One
   system for capacity and consumption billing.

7. **Throughput** — pipeline handles ~1,700 events/second sustained on a
   laptop. Realistic sovereign cloud load is ~17 events/s (100x headroom).

8. **OSAC readiness** — OSAC doesn't emit model events yet. When it does,
   we add a Model case to the Watch stream dispatcher. The ingest endpoint
   is for testing; in production, events come from the Watch stream.
