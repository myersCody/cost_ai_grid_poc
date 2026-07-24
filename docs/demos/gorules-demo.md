# Demo: GoRules Programmable Rating

> Recorded demo — instance-type pricing and committed-use discounts
> using a JSON decision engine. No code changes for pricing updates.

## Prerequisites

```bash
source snippets/env.sh
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
bash ../snippets/create-test-data.sh   # populate OSAC with VMs
```

OSAC + PostgreSQL running, token fresh in `/tmp/osac_token.txt`.

## Setup

Start the consumer with rule engine enabled:

```bash
RULES_DIR=rules ./inventory-watcher
```

Log should show: `rule engine enabled rules_dir=rules`

Open two browser windows:
1. **Debug dashboard**: http://localhost:8020/debug/dashboard
2. **GoRules demo page**: http://localhost:8020/demo/gorules

---

## Recording Flow

### Part 1: Show the rules (no running service needed)

Open `rules/compute-pricing.json` and explain the decision table:

```
Instance Type × Tenant Tier → Price/Hour + Discount

standard-2-8  + gold     → $0.10/hr, 20% off = $0.08 effective
standard-4-16 + gold     → $0.20/hr, 20% off = $0.16 effective
standard-4-16 + standard → $0.20/hr, 0% off  = $0.20 effective
standard-8-32 + gold     → $0.40/hr, 20% off = $0.32 effective
```

Key point: "This is a JSON file. Not Go code. An operator edits it,
restarts, and pricing changes. No PR. No recompile."

Then show `rules/committed-use-pricing.json` — the 3-node graph:

```
CUD Lookup → Sustained-Use Tier → Calculate Final Cost

tenant-acme:  5 VMs committed, 40% CUD discount
tenant-globex: 10 VMs committed, 50% CUD discount
Over-commitment → sustained-use discounts (5-30% based on utilization)
No commitment → on-demand pricing
```

Key point: "This decision graph chains three nodes. A business analyst
can modify any node independently. Try doing this with if/else in Go."

### Part 2: Live demo via the demo page

In the **GoRules demo page** (http://localhost:8020/demo/gorules):

1. **Paste the OSAC token** into the token field at the top

2. **Stage 1: Reconcile** — click Run
   - Watch the debug dashboard counter update
   - "We sync VMs and tenants from OSAC"

3. **Stage 2: Set tenant tiers** — click Run
   - This PATCHes the first tenant in OSAC with `cost-mgmt/tier=gold`
   - Then triggers reconcile to sync labels
   - "We set the tier as a label on the tenant via the OSAC API.
     Our system reads it during rating."

4. **Wait ~90 seconds** for metering (60s) + rating (30s) sweeps
   - Watch the debug dashboard: metering entries appear, then cost entries
   - Point out the log: `rule engine rated entry ... instance_type=standard-4-16 tenant_tier=gold cost=0.16`

5. **Stage 4: Cost by tenant** — click Run
   - Gold tenant shows lower costs than standard tenants
   - "Same VMs, different price. The rule engine applied the gold discount."

6. **Stage 5: Cost by resource** — click Run
   - Each VM shows different costs based on instance type
   - "standard-8-32 costs 4× more than standard-2-8. Instance-type awareness."

### Part 3: Change the rules live (most powerful moment)

1. Edit `rules/compute-pricing.json` — change gold discount from 20% to 40%
2. Restart the consumer: `RULES_DIR=rules ./inventory-watcher`
3. Wait for the next rating sweep
4. Show the cost report again — gold tenant costs dropped further

"Pricing change in 30 seconds. No developer needed."

---

## Talking Points

1. **Rates as data, not code** — JSON decision tables, versionable in git
2. **Instance-type awareness** — different SKUs, different prices
3. **Tenant tier discounts** — gold/standard via OSAC labels
4. **Committed-use agreements** — multi-node decision graph with CUD
   lookup, sustained-use fallback, expression-based cost calculation
5. **Graceful fallback** — rule engine failure → static rates, no disruption
6. **Performance** — sub-microsecond evaluation per entry (Rust/Zen compiled)
7. **Visual editor** — GoRules has an open-source React editor for
   decision tables (editor.gorules.io)

## What to Show on Screen

| Window | Content |
|---|---|
| Browser 1 | Debug dashboard — live counters updating |
| Browser 2 | GoRules demo page — click through stages |
| Editor (optional) | `compute-pricing.json` open for the "change rules live" moment |
| Terminal (optional) | Consumer logs showing `rule engine rated entry` lines |
