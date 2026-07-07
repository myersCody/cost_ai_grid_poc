# GoRules Demo Scenarios — OSAC Flow

Demo ideas for programmable rating with GoRules/Zen engine on capacity-based
OSAC resources (not MaaS).

## Scenarios

1. **Instance-type-based pricing** — `standard-4-16` VM costs more per hour
   than `standard-2-8`. Rule looks up instance type from inventory and
   applies different rates per SKU.

2. **Tenant tier discounts** — "tenant-acme is Gold tier, gets 20% off.
   tenant-initech is Bronze, pays full price." Rule checks tenant metadata
   or a tenant-tier lookup table.

3. **Volume-based discounts** — "first 100 VM-hours/month free, next 1000
   at $0.10/hr, unlimited at $0.08/hr." Tiered but driven by accumulated
   monthly consumption, not just the current entry's value.

4. **Time-of-day pricing** — "off-peak hours (18:00–06:00) at 50% rate."
   Rule checks the metering period timestamp.

5. **Cluster size surcharge** — "clusters with >10 worker nodes get a
   management fee of $5/hr on top of per-node cost."

## Priority for demo

Start with #1 (instance-type pricing) and #2 (tenant discounts) — both
are visual, easy to explain, and show clear business value. The decision
table format makes them intuitive in the GoRules editor.
