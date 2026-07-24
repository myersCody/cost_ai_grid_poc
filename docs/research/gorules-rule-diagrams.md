# GoRules Decision Logic

## Rule 1: Instance-Type Pricing with Tenant Discounts

```
┌─────────┐     ┌──────────────────────────┐     ┌──────────────────┐     ┌──────────┐
│  Input   │────▶│  Instance Type Rate      │────▶│  Calculate Cost  │────▶│  Output  │
│          │     │  (decision table)        │     │  (expression)    │     │          │
│ instance │     │                          │     │                  │     │ cost     │
│ tenant   │     │ instance_type × tier     │     │ value/3600       │     │ rate     │
│ value    │     │    → price + discount    │     │   × price        │     │ currency │
└─────────┘     └──────────────────────────┘     │   × (1-disc%)    │     └──────────┘
                                                  └──────────────────┘
```

### Decision Table

| Instance Type  | Tenant Tier | $/Hour | Discount | Effective |
|----------------|-------------|--------|----------|-----------|
| standard-2-8   | gold        | $0.10  | 20%      | $0.08/hr  |
| standard-2-8   | *(any)*     | $0.10  | 0%       | $0.10/hr  |
| standard-4-16  | gold        | $0.20  | 20%      | $0.16/hr  |
| standard-4-16  | *(any)*     | $0.20  | 0%       | $0.20/hr  |
| standard-8-32  | gold        | $0.40  | 20%      | $0.32/hr  |
| standard-8-32  | *(any)*     | $0.40  | 0%       | $0.40/hr  |
| *(any)*        | *(any)*     | $0.10  | 0%       | $0.10/hr  |

### Examples

| VM              | Tenant       | Tier   | Duration | Cost     |
|-----------------|-------------|--------|----------|----------|
| standard-4-16   | tenant-acme | gold   | 1 hour   | **$0.16** |
| standard-4-16   | tenant-init | bronze | 1 hour   | **$0.20** |
| standard-8-32   | tenant-acme | gold   | 1 hour   | **$0.32** |
| standard-2-8    | tenant-acme | gold   | 1 min    | **$0.001** |

---

## Rule 2: Committed-Use with Sustained-Use Fallback

```
┌─────────┐     ┌──────────────────┐     ┌─────────────────────┐     ┌─────────────────┐     ┌──────────┐
│  Input   │────▶│  CUD Agreement   │────▶│  Sustained-Use      │────▶│  Calculate      │────▶│  Output  │
│          │     │  Lookup          │     │  Discount           │     │  Final Cost     │     │          │
│ tenant   │     │  (decision table)│     │  (decision table)   │     │  (expression)   │     │ cost     │
│ resource │     │                  │     │                     │     │                 │     │ rate     │
│ value    │     │ tenant_id        │     │ utilization %       │     │ if within CUD:  │     │ desc     │
│ vms      │     │   → committed   │     │   → sustained       │     │   use CUD disc  │     │          │
│ util %   │     │     qty + disc % │     │     discount %      │     │ else:           │     │          │
│ base $/h │     │                  │     │                     │     │   use sustained │     │          │
└─────────┘     └──────────────────┘     └─────────────────────┘     └─────────────────┘     └──────────┘
```

### Node 1: CUD Agreement Lookup

| Tenant         | Resource Type    | Committed VMs | CUD Discount | Type   |
|----------------|------------------|---------------|--------------|--------|
| tenant-acme    | compute_instance | 5             | 40%          | 1-year |
| tenant-globex  | compute_instance | 10            | 50%          | 1-year |
| *(any)*        | *(any)*          | 0             | 0%           | none   |

### Node 2: Sustained-Use Discount

| Monthly Utilization | Sustained Discount |
|---------------------|-------------------|
| ≥ 100%              | 30%               |
| ≥ 75%               | 20%               |
| ≥ 50%               | 10%               |
| ≥ 25%               | 5%                |
| *(any)*             | 0%                |

### Node 3: Final Cost Calculation

```
if running_vms ≤ committed_qty:
    discount = CUD discount (40-50%)
    label = "CUD 1-year"
else if monthly_utilization ≥ 25%:
    discount = sustained-use discount (5-30%)
    label = "Sustained-use"
else:
    discount = 0%
    label = "On-demand"

cost = (seconds / 3600) × base_price × (1 - discount%)
```

### Examples

| Tenant        | Running VMs | Committed | Utilization | Base  | Discount | Effective | Cost/hr  | Label          |
|---------------|-------------|-----------|-------------|-------|----------|-----------|----------|----------------|
| tenant-acme   | 3           | 5         | 80%         | $0.20 | 40% CUD  | $0.12     | **$0.12** | CUD 1-year     |
| tenant-acme   | 8           | 5         | 80%         | $0.20 | 20% sust | $0.16     | **$0.16** | Sustained-use  |
| tenant-initech| 2           | 0         | 10%         | $0.20 | 0%       | $0.20     | **$0.20** | On-demand      |
| tenant-globex | 8           | 10        | 95%         | $0.40 | 50% CUD  | $0.20     | **$0.40**/2hr | CUD 1-year |
