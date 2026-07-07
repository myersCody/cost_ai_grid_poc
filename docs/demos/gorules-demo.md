# Demo: GoRules Programmable Rating

> Instance-type pricing with tenant tier discounts — rates defined in a
> JSON decision table, not code.

## Setup

```bash
source snippets/env.sh
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
```

`snippets/env.sh` sets:
```bash
export OSAC_BASE_URL=http://localhost:8011
export OSAC_TOKEN=$(cat /tmp/osac_token.txt)
export INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb
export INGEST_LISTEN_ADDR=localhost:8020
export BASE="$OSAC_BASE_URL"
export TOKEN="$OSAC_TOKEN"
export DB="docker exec cost-db psql -U user -d costdb -c"
```

Prerequisites: OSAC + PostgreSQL running, token fresh.

---

## Act 1: Populate OSAC with test data

```bash
bash snippets/create-test-data.sh
```

This creates 3 instance types and 3 VMs in OSAC.

---

## Act 2: Start consumer WITH rule engine

```bash
RULES_DIR=rules ./inventory-watcher
```

**Point out in logs:**
- `rule engine enabled rules_dir=rules`
- `reconciled compute instances osac_count=N`

---

## Act 3: Set tenant tiers

After reconciliation syncs tenants, tag them:

```bash
$DB "UPDATE inventory_tenant SET labels = labels || '{\"tier\": \"gold\"}' WHERE tenant_id = 'shared';"
$DB "SELECT tenant_id, labels->>'tier' as tier FROM inventory_tenant;"
```

**Show:** `shared` is gold, others are standard (no tier label).

---

## Act 4: Inspect the decision table

Open `rules/compute-pricing.json` or show in the presentation:

```
┌──────────────────┬─────────────┬──────────────┬────────────┐
│ Instance Type     │ Tenant Tier │ Price/Hour $ │ Discount % │
├──────────────────┼─────────────┼──────────────┼────────────┤
│ standard-2-8     │ gold        │ 0.10         │ 20         │
│ standard-2-8     │ (any)       │ 0.10         │ 0          │
│ standard-4-16    │ gold        │ 0.20         │ 20         │
│ standard-4-16    │ (any)       │ 0.20         │ 0          │
│ standard-8-32    │ gold        │ 0.40         │ 20         │
│ standard-8-32    │ (any)       │ 0.40         │ 0          │
│ (any)            │ (any)       │ 0.10         │ 0          │
└──────────────────┴─────────────┴──────────────┴────────────┘
```

**Point out:** "This is a JSON file, not code. An operator can edit it,
restart, and pricing changes. No PR, no recompile."

---

## Act 5: Wait for metering + rating sweeps

Wait ~90 seconds (metering 60s + rating 30s).

**Watch the logs for:**
```
rule engine rated entry resource=vm-xxx instance_type=standard-4-16 tenant_tier=gold cost=... effective_rate=0.16
```

---

## Act 6: Compare costs — gold vs standard

```bash
$DB "
SELECT ci.name, ci.instance_type, ce.tenant_id,
       round(ce.cost_amount::numeric, 6) as cost,
       ce.currency
FROM cost_entries ce
JOIN inventory_compute_instance ci ON ce.resource_id = ci.instance_id
WHERE ce.meter_name = 'vm_uptime_seconds'
ORDER BY ci.instance_type, ce.tenant_id
LIMIT 20;
"
```

**Point out:** Same instance type, different cost for gold vs standard tenant.

---

## Act 7: Show it in Bruno

Open Bruno → **Cost Report (JSON)**:
- `group_by=tenant` → gold tenant has lower total cost
- `group_by=resource` → each VM shows its individual cost

---

## Act 8: Change the rules live

Edit the decision table — add a "platinum" tier with 40% discount:

```bash
# Add to rules/compute-pricing.json, restart consumer
# OR just show the concept:
echo "To add a new tier:"
echo "  1. Edit rules/compute-pricing.json"
echo "  2. Add a row: standard-4-16 | platinum | 0.20 | 40"
echo "  3. Restart the consumer"
echo "  No code change. No PR. No recompile."
```

---

## Talking Points

1. **Rates as data, not code** — the decision table is a JSON file.
   Change pricing without touching Go code.

2. **Instance-type awareness** — a `standard-8-32` VM costs 4× more
   than `standard-2-8`. The current flat-rate model charges the same
   per uptime second regardless of size.

3. **Tenant tier discounts** — gold tenants get 20% off automatically.
   The tier comes from tenant labels — set by an admin, applied by
   the rule engine.

4. **Graceful fallback** — if the rule engine fails or isn't configured,
   the existing static rate system takes over. No disruption.

5. **Performance** — GoRules/Zen compiles rules to native code via Rust.
   Sub-microsecond evaluation per entry. No impact on the 30s rating
   sweep.

6. **Visual editor** — GoRules has an open-source React UI for editing
   decision tables visually (editor.gorules.io). The JSON format is
   compatible.
