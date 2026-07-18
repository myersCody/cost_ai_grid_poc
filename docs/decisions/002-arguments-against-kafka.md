# Arguments Against Kafka for the Cost Management PoC

## Context

The requirements brief lists Kafka as the "likely transport" for CloudEvents
between OSAC and Cost Management, but also flags this as an open question.
The inventory-watcher currently consumes events via the OSAC fulfillment-service
gRPC Watch stream (exposed as NDJSON over the REST gateway) and reconciles
periodically via List endpoints.

This document argues that Kafka is unnecessary for the PoC and likely
unnecessary for production, given the current architecture.

## What the Watch stream + reconciler already provides

| Concern | Kafka solution | Watch stream + reconciler solution |
|---|---|---|
| **Event delivery** | Consumer reads from topic | Client receives streamed events |
| **Missed events** | Replay from offset | Reconciler diffs against List endpoints |
| **Ordering** | Per-partition ordering | Single stream, in-order |
| **Restart recovery** | Resume from committed offset | `last_metered_at` tracks exactly where metering left off |
| **Deduplication** | Consumer-side (still needed) | `raw_events` table with UNIQUE on `event_id` (Note: the unique index was later dropped for throughput — see [ADR-004](004-raw-events-no-unique-index.md)) |
| **At-least-once delivery** | Built-in with offset management | Reconciler guarantees eventual consistency |

The Watch stream + reconciler achieves the same delivery guarantees as Kafka
with consumer-side offset tracking. Both require the consumer to handle
deduplication. Both require the consumer to handle restarts. The difference
is where the complexity lives.

## Arguments against Kafka

### 1. No need for multiple consumers

Kafka's primary value is fan-out: one event stream consumed independently by
multiple services, each at their own pace. In the OSAC + Cost Management
architecture, there is **one consumer** (the cost service) for these events.
There is no second consumer today and no concrete plan for one.

If a second consumer appears in the future, Kafka can be added at that point.
Adding it now is speculative infrastructure.

### 2. Operational overhead

Kafka is not a simple dependency:

- **Cluster management** — even KRaft-mode Kafka (no ZooKeeper) requires
  broker configuration, topic creation, partition management, replication
  factor decisions
- **Monitoring** — consumer lag, broker health, disk usage, under-replicated
  partitions
- **Upgrades** — Kafka version upgrades require rolling restarts and
  compatibility checks
- **Disk** — Kafka persists all messages for the retention period; for
  high-frequency metering events (every 60s per resource), storage grows
  quickly
- **Expertise** — operating Kafka reliably requires specific knowledge that
  may not be available on the cost management team

For a PoC with a July 31, 2026 deadline, this overhead directly competes
with feature development time.

### 3. Latency

The Watch stream delivers events with **sub-second latency** — the gRPC
stream pushes the event as soon as the PostgreSQL NOTIFY fires. There is no
broker hop, no consumer poll interval, no batch flush delay.

With Kafka, the path is longer:

```
Resource mutation → PostgreSQL → OSAC producer → Kafka broker → Cost consumer
```

Each hop adds latency: producer batching (default: up to 1 second or
`linger.ms`), broker write, consumer poll interval (default: 500ms). Under
typical settings, end-to-end latency is 1-3 seconds. This still meets the
60-second SLA, but the Watch stream meets it with margin to spare and fewer
failure modes.

### 4. Failure modes

Kafka introduces failure modes that don't exist with direct streaming:

- **Broker unavailable** — events are lost or delayed until the broker
  recovers (OSAC would need to buffer or retry)
- **Consumer lag** — if the consumer falls behind, it must process a backlog
  before it can serve real-time data
- **Offset management bugs** — incorrect offset commits can cause duplicate
  processing or event loss
- **Topic misconfiguration** — wrong partition count, retention, or
  replication can cause silent data loss

With the Watch stream, the failure mode is simple: if disconnected, events
are missed; the reconciler catches up. No intermediate system can fail.

### 5. The reconciler already solves the durability problem

The main argument for Kafka is durability: "what if events are lost?" But
events can be lost with Kafka too (producer failures, broker crashes before
replication). Both architectures need a reconciliation mechanism.

We already have one. The reconciler calls OSAC's List endpoints and diffs
against inventory state. This is the same pattern OSAC's own documentation
recommends: *"Clients should consider using other mechanisms to ensure that
they process objects correctly. For example, they can combine this watch
mechanism with periodic reconciliation of all the objects."*

### 6. The gRPC Watch interface already exists and works

The OSAC fulfillment-service already provides a production-quality gRPC
Watch streaming API with CEL-based filtering, authentication, and full
resource payloads. It's a first-class, documented, tested interface — not a
hack or a workaround. This is how OSAC is designed to be consumed.

There is no Kafka producer in OSAC today. Adding Kafka would require OSAC
to implement a producer, define topic schemas, manage serialization — new
work on the OSAC side that is not planned or committed. We would be asking
another team to build infrastructure so we can avoid using the interface
they already built.

Using what exists is faster, simpler, and has zero cross-team dependencies.

### 7. Cost

For a self-managed (on-premise) deployment:

- Kafka requires dedicated broker nodes (minimum 3 for production)
- Persistent storage for message retention
- Network bandwidth for replication
- Monitoring infrastructure

The Watch stream requires nothing beyond the OSAC service that's already
running.

For a cloud/managed deployment (e.g., MSK, Confluent Cloud), Kafka adds a
per-hour and per-GB cost that scales with event volume.

### 8. This is the proven Kubernetes pattern

The Watch stream + List reconciliation pattern is not something we invented.
It is the standard pattern used by every Kubernetes controller, operator, and
client — the same pattern that manages millions of clusters in production
worldwide.

The Kubernetes informer framework works exactly this way:

1. **List** all resources of a type to build initial state
2. **Watch** for changes going forward
3. On disconnection, **re-list** to catch up on missed events
4. Use a **resource version** to avoid processing stale data

This pattern has been battle-tested at massive scale (thousands of nodes,
hundreds of thousands of pods) for over a decade. It works without Kafka,
without a message broker, without any intermediate system.

OSAC itself is built on this pattern — its controllers use the same
List + Watch approach internally. Using the same pattern from the cost
consumer is architecturally consistent with the rest of the ecosystem.

## When Kafka would make sense

Kafka is the right choice when:

- **Multiple independent consumers** need the same event stream (e.g., cost
  management + audit logging + analytics + a third-party integration)
- **Cross-datacenter replication** is required (Kafka MirrorMaker)
- **Event replay over days/weeks** is a requirement (not just catch-up on
  restart)
- **Guaranteed ordering across partitions** with complex routing is needed
- **OSAC commits to producing Kafka events** as a first-class transport

None of these conditions hold today.

## Recommendation

Use the Watch stream + reconciler for the PoC and likely for production v1.
If multiple consumers emerge or OSAC standardizes on Kafka as a transport,
the migration path is straightforward: replace the Watch stream reader with
a Kafka consumer. The `raw_events` table, metering pipeline, and inventory
store remain unchanged — only the event source changes.

The architecture is transport-agnostic by design. The OSAC client
(`internal/osac/client.go`) is the only component that knows about the Watch
stream. Swapping it for a Kafka consumer is a localized change.
