# Ingestion modes

The service has three runtime modes. All three share the same downstream cost
pipeline after an event enters the consumer:

```text
event input → raw_events/inventory → metering sweep → rating sweep → cost entries
```

The current primary path is batch ingestion. The OSAC Watch/reconciler path is
retained for local development and fallback operation. Kafka is an opt-in,
temporary experiment.

## 1. Direct OSAC Watch and reconciliation

This is the original full flow:

```text
OSAC Watch ───────────────┐
                          ├─→ inventory/raw events → metering → rating
OSAC List reconciliation ─┘
```

The watcher receives lifecycle events and processes them directly. The
reconciler periodically reads OSAC List endpoints and repairs missed or stale
inventory state. No Kafka broker is required.

Typical configuration leaves `watcher` and `reconciler` enabled and does not
set `KAFKA_BROKERS`:

```text
OSAC_BASE_URL=http://localhost:8011
OSAC_TOKEN=...
RECONCILE_INTERVAL=1h
```

## 2. Kafka experiment

The intended temporary Kafka experiment changes the OSAC event handoff to:

```text
OSAC Watch/reconciliation → Kafka producer → cost consumer → metering/rating
```

When a Kafka producer is configured, the watcher publishes Watch events and
does not persist or process them directly. The Kafka consumer owns raw-event
persistence, inventory updates, and event-driven metering.

The current reconciler still writes reconciliation corrections directly to
inventory; it does not yet emit those corrections to Kafka. Consequently,
enabling the reconciler during the experiment creates a hybrid path. The exact
Watch/reconciliation → Kafka topology requires the reconciler to publish its
corrections through the same producer.

The modes are selected with `KAFKA_MODE`:

| `KAFKA_MODE` | Producer | Consumer | Intended use |
|---|---:|---:|---|
| `producer` | yes | no | Watcher process publishes to Kafka for another consumer |
| `consumer` | no | yes | Consumer process reads Kafka; disable the local watcher |
| `both` | yes | yes | One process runs the Watch-to-Kafka-to-consumer loop |

All Kafka modes require `KAFKA_BROKERS`. A consumer-only process should set:

```text
KAFKA_BROKERS=localhost:19092
KAFKA_MODE=consumer
DISABLE_COMPONENTS=watcher,reconciler
```

Until reconciliation publishing is implemented, a pure Kafka run must disable
the reconciler. Routing reconciliation corrections through Kafka is a separate
follow-up implementation change.

The legacy `POST /api/v1/events` endpoint and the batch endpoint are not Kafka
producer paths.

## 3. Batch ingestion API

This is the current primary OSAC delivery path:

```text
OSAC adapter → POST /api/v1/events/batch → direct transactional processing
                                           → metering/rating
```

The adapter submits canonical OSAC CloudEvents in batches of 1–100 events.
Receipt claims, raw-event persistence, inventory updates, and event-driven
metering commit in one transaction. The endpoint does not use Kafka, the
watcher, or the reconciler.

A batch-only process should disable the retained OSAC components and leave
Kafka unset:

```text
INGEST_LISTEN_ADDR=:8020
DISABLE_COMPONENTS=watcher,reconciler
```

See the [batch ingestion contract](requirements/osac-batch-ingest-contract.md)
and [API reference](api-reference.md).

## Runtime controls

| Variable | Purpose |
|---|---|
| `DISABLE_COMPONENTS` | Comma-separated components such as `watcher,reconciler` |
| `KAFKA_BROKERS` | Enables the optional Kafka experiment when non-empty |
| `KAFKA_MODE` | Kafka mode: `producer`, `consumer`, or `both` |
| `KAFKA_CONSUMER_GROUP` | Kafka consumer group name |
| `KAFKA_TOPIC_PREFIX` | Kafka topic prefix, default `osac.metering` |
| `INGEST_LISTEN_ADDR` | Enables the HTTP ingestion API |
