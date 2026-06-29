# Input: Requirements Update from Cody — June 27, 2026

## Requirements Source of Truth

Pau's document is the source of truth for requirements. Lili confirmed this.
More meetings are coming and things will likely shift as discussions continue.

Cody merged both Pau's and Lili's docs into a consolidated requirements
overview, keeping Pau's determinations as authoritative:

- **[poc_requirements_overview.md](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md)** —
  consolidated requirements v1.1 (the current reference)

Implementation-detail language (pushing HTTP vs Kafka vs gRPC, pushing
FastAPI) was cleaned up to state the need ("we need to communicate") rather
than prescribing the solution. This preserves our freedom to make
architectural decisions.

## Heartbeat Events — Confirmed

Cody confirmed through a meeting transcript that **"heartbeat events" refer
to the events produced by the OSAC metering collector PoC**
(`osac-metering-discover-poc`) — not a new event type.

This aligns with our implementation:
- [ADR-003: Heartbeat Emitter vs Local Sweep](../decisions/003-heartbeat-emitter-vs-sweep.md)
  documents the distinction and our decision to use a local 60s sweep for
  the PoC
- Our OpenMeter-compatible ingest endpoint accepts the same CloudEvents the
  collector produces

Related docs from Cody:
- [ADR-003](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/decisions/003-heartbeat-emitter-vs-sweep.md)
- [Metering spec draft](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/poc_architecture/metering/metering-spec-draft.md)

## Missing Events from OSAC

Cody compiled the list of events we need from OSAC that don't exist yet.
To discuss with Moti on Monday:

- **[event-types.md #Required from OSAC](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/poc_architecture/event-types.md#required-from-osac)**

From our own analysis, the key gaps are:
- `BareMetalInstance` not in the Watch stream `oneof` payload
  (see [req8 gap analysis](../req8-bare-metal-gap-analysis.md))
- No `Model` entity in OSAC at all (MaaS)
- No BMaaS or MaaS metering collector scripts

## Boundary Monitoring (Quotas / Alerts)

Cody's initial design for notifications/alerts on budgets and quotas:

- **[boundary_monitoring/](https://github.com/myersCody/cost_ai_grid_poc/tree/main/docs/poc_architecture/boundary_monitoring)**

Our current state:
- REQ-9 (Quota status API) — **Done**: `GET /api/v1/quotas/{tenant_id}`
  with threshold checks at 50/70/90/100%
- REQ-10 (Threshold notifications) — **Not started**: need to review
  Cody's design and agree on transport (webhook, CloudEvent, pull-only)
