# Rating Engine Options

> **Source:** Input from Pau (Jun 27, 2026) on rating engine alternatives
> used in the OpenStack and open-source billing ecosystem.

## Context

The metering pipeline produces `metering_entries` rows:
`(resource_id, tenant_id, meter_name, value, unit, period_start, period_end)`.

The rating step turns these into cost:
`cost = apply_rate(meter_name, value, tenant_id) → cost_amount`.

This is req #6 (Cost Tiers) — support tiered pricing like "first 20 GiB
free, next 100 GiB at $0.08, rest at $0.07" for both capacity-based
(CaaS/VMaaS) and consumption-based (MaaS) rates.

The question: how should rates be defined and evaluated?

## Options Evaluated

### 1. CloudKitty (OpenStack)

Two rating modes:

**Hashmap** — predefined rates in a custom YAML-like format. Maps metrics
to flat or tiered prices. Equivalent to Cost Management's current rate
model (JSON-based cost models).

**PyScript** — exposes metrics via a custom `object.property` model, and
you implement rating logic in Python. Runs in a MicroPython/WASM sandbox.
Equivalent to what OpenMeter does (write code in a supported SDK language).

**Relevance:** The two-tier approach (declarative for simple rates, code
for complex ones) is a good pattern. The specific implementation (Python
WASM sandbox) is less important than the model of making metrics available
to a programmable rating engine.

### 2. GoRules / Zen Engine

- Written in Rust, not Go (despite the name)
- Uses **JDM** (JSON Decision Model) — a pseudo-standard they created
- Rates and math defined in JSON, compiled to native code by the open-source
  **Zen engine**
- Ships with an open-source **React UI editor** (JDM Editor) for visual
  rule design
- Extremely fast execution (compiled rules)
- Closest open-source alternative to commercial rating platforms (e.g., M360)
  that offer visual metric/rate design

**Pros:**
- Very fast (native compiled rules)
- Visual editor for non-developers to define rates
- Open source (MIT license)
- JSON-based rules are easy to version control and audit

**Cons:**
- Rust dependency (though it has Go bindings)
- JDM is their own format, not an industry standard
- Smaller community than Drools

### 3. Drools

- De facto standard in open-source business rules engines
- Java-based, very mature, large community
- Was a Red Hat project (moved to IBM between 2023-2025)
- May still have business rules expertise at Red Hat

**Disqualified for AI Grid:** Startup time is seconds to minutes. The
60-second processing SLA means we can't afford a cold-start penalty on
the rating engine. Drools is designed for long-running JVM processes, not
the lightweight, fast-startup model we need.

### 4. Smaller Projects (json-rules-engine, etc.)

Lightweight JSON-based rule engines exist in various languages. Not
recommended — small maintainer base, risk of abandonment.

## Analysis

| Option | Speed | UI Editor | Language | Maturity | Risk |
|---|---|---|---|---|---|
| CloudKitty hashmap | Medium | No | Python/YAML | Mature (OpenStack) | Tied to OpenStack ecosystem |
| CloudKitty PyScript | Medium | No | Python/WASM | Mature | WASM sandbox complexity |
| GoRules/Zen | Very fast | Yes (React) | Rust + bindings | Growing | JDM is proprietary format |
| Drools | N/A | Yes | Java | Very mature | Startup time disqualifies |
| json-rules-engine | Fast | No | JS | Fragile | May die |
| Custom (SQL/Go) | Fast | No | Go/SQL | N/A | Must build everything |

## Recommendation for PoC

For the July 31 deadline, start with the simplest thing that works:

**Phase 1 (PoC): SQL-based rating with JSON rate definitions.**
The `rates` table already defined in the data model supports flat and tiered
pricing via a `tiers` JSONB column. Rating logic is a Go function that takes
a metering entry + rate definition and computes cost. No external engine.

```go
func ApplyRate(meter MeteringEntry, rate Rate) float64 {
    if rate.Tiers == nil {
        return meter.Value * rate.PricePerUnit
    }
    return applyTieredRate(meter.Value, rate.Tiers)
}
```

This covers the requirements for PoC: flat rates, tiered rates, per-tenant
rate overrides.

**Phase 2 (post-PoC): Evaluate GoRules/Zen for custom metrics (req #7).**
Requirement #7 asks for "custom rates from arbitrary metrics" — this is
where a programmable rule engine adds value. GoRules is the strongest
candidate: fast, visual editor, open source, JSON-based rules.

The two-phase approach mirrors CloudKitty's model: declarative for standard
rates, programmable for custom ones. But we use Go/SQL for phase 1 instead
of Python/YAML, and GoRules/Zen for phase 2 instead of PyScript/WASM.

## Key Insight

The rating engine choice doesn't affect the metering pipeline. Metering
entries are the input regardless of how rates are evaluated. We can start
simple and swap in a more capable engine later without touching the event
ingestion, inventory, or metering code.
