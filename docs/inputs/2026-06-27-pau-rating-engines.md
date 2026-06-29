# Input: Rating Engine Research from Pau — June 27, 2026

## Summary

Pau provided an overview of rating engine options from the OpenStack and
open-source billing ecosystem. This informed our rating engine decision
for the PoC (SQL-based) and post-PoC (GoRules/Zen candidate).

Full analysis: [docs/research/rating-engine-options.md](../research/rating-engine-options.md)

## Options Evaluated

**CloudKitty (OpenStack):**
- **Hashmap** — predefined rates in YAML-like format. Equivalent to Cost
  Management's current rate model.
- **PyScript** — metrics exposed via a custom object.property model,
  implemented in Python (MicroPython in WASM sandbox). Equivalent to
  OpenMeter's approach (write code in an SDK language). The language is
  less important than how metrics are made available to it.

**GoRules / Zen Engine:**
- Written in Rust (not Go despite the name)
- Uses JDM (JSON Decision Model) — their own pseudo-standard
- Open-source React UI editor (JDM Editor)
- Rules compiled to native code — extremely fast
- Closest open-source alternative to M360 for visual custom metric design

**Drools:**
- De facto open-source standard for business rules
- Was a Red Hat project (moved to IBM 2023-2025)
- **Disqualified:** Java startup time is seconds to minutes — unusable
  given the 60-second processing SLA
- May still find business rules expertise at Red Hat

**Smaller projects (json-rules-engine, etc.):**
- Not recommended — small maintainer base, risk of abandonment

## Our Decision

- **PoC:** SQL-based rating with JSON rate definitions (`rates` table +
  `tiers` JSONB). Implemented and working.
- **Post-PoC (REQ-13):** GoRules/Zen is the strongest candidate for
  custom rate dimensions — fast, visual editor, open source.
