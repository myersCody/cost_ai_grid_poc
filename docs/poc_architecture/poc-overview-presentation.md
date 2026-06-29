# Cost Management for AI Grid — POC Overview

---

## What Are We Building?

A **Cost Management** system that plugs into the AI Grid sovereign cloud — tracking resource usage, calculating costs, and alerting when tenants approach budget limits.

```
AI Grid  →  OSAC  →  Cost Management  →  Reports & Alerts
```

---

## The Three Systems

```mermaid
flowchart LR
    subgraph A ["1 · AI Grid"]
        res["Clusters · VMs\nAI Models · Bare Metal"]
    end

    subgraph B ["2 · OSAC\n(Open Sovereign AI Console)"]
        osac["Orchestrator\nProvisions & manages\nall resources"]
    end

    subgraph C ["3 · Cost Management POC"]
        cost["Tracks usage\nCalculates cost\nEnforces quotas"]
    end

    A -->|"runs on"| B
    B -->|"streams events\nto"| C
    C -->|"quota alerts\nback to"| B
```

---

## How Data Flows (Simple View)

```mermaid
flowchart LR
    osac["OSAC\nResource Events"]
    watcher["inventory-watcher\nGo service"]
    db[("Database\ncostdb")]
    api["Cost API"]
    ui["Reports & UI"]

    osac -->|"What was created,\nupdated, or deleted"| watcher
    watcher -->|"Store inventory\n& usage meters"| db
    db -->|"Query costs\n& usage"| api
    api --> ui
```

---

## What Gets Tracked

| Resource | Type | How We Meter It |
|---|---|---|
| **Cluster** (OCP) | Capacity | Time cluster is running × node count |
| **VM** (OpenShift Virt) | Capacity | Time VM is running × CPU cores / memory |
| **AI Model** (OpenShift AI) | Consumption | Tokens in + tokens out + requests |
| Bare Metal | — | Not in scope for POC |

---

## The Metering Pipeline

```mermaid
flowchart LR
    event["OSAC sends\nresource event"]
    inventory["Inventory updated\n(what exists, what state)"]
    meter["Every 60s\nMeter sweep\nhow long was it running?"]
    rate["Every 30s\nRater\ncost = usage × rate"]
    report["Cost entries\nready to query"]

    event --> inventory --> meter --> rate --> report
```

1. **OSAC** tells us when a resource is created, changed, or deleted
2. **inventory-watcher** keeps a live inventory of all resources
3. Every **60 seconds** — calculate how long each resource has been running
4. Every **30 seconds** — apply pricing rates → produce cost rows
5. **Cost API** serves those rows to dashboards and reports

---

## Two Ways Events Arrive

```mermaid
flowchart TB
    subgraph today ["Today (POC)"]
        stream["Watch stream\ncontinuous NDJSON\nfrom OSAC"]
        sweep["Local meter sweep\nevery 60s — Cost\ncalculates duration"]
        stream --> sweep
    end

    subgraph phase4 ["Phase 4 (Production)"]
        collector["OSAC Metering Collector\npre-calculates duration\nevery 60s"]
        ingest["HTTP ingest endpoint\nPOST /api/v1/events\nalready built and ready"]
        collector -->|"CloudEvent push\n(just needs URL redirect)"| ingest
    end
```

> The POC self-meters today. In production, OSAC pushes pre-calculated usage — the endpoint is already built.

---

## Quota Alerts

```mermaid
flowchart LR
    usage["Tenant usage\naccumulates"]
    check{"Usage >\nthreshold?"}
    alert["Alert emitted\nto OSAC"]
    policy["OSAC applies\nrate-limit policy"]
    ok["No action"]

    usage --> check
    check -->|"Yes (70% or 90%)"| alert --> policy
    check -->|"No"| ok
```

---

## POC Scope Summary

| Capability | Status |
|---|---|
| Inventory sync from OSAC watch stream | **Done** |
| Cluster metering (capacity-based) | **Done** |
| VM metering (capacity-based) | **Done** |
| Pricing / cost calculation | **Done** |
| AI Model metering (consumption-based) | **Partial** |
| HTTP ingest endpoint (Phase 4 ready) | **Done** |
| Cost API + Reports | In progress |
| Quota alerts to OSAC | Planned |
| Bare Metal metering | Blocked (OSAC schema TBD) |

---

## What We Need from OSAC (Phase 4)

Only **one action** is required on the OSAC side to move from POC to production metering:

> **Redirect the metering collector's target URL from OpenMeter to the Cost Management endpoint.**

No schema changes. No format translation. The endpoint is built and verified.
