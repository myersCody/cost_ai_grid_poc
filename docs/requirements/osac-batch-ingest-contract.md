# OSAC batch ingestion contract

## Status

Approved implementation contract for the primary Cost Management adapter proof
of concept. The adapter delivers canonical OSAC CloudEvents to this HTTP
receiver. The separate direct Kafka consumer is a temporary experiment and is
not part of this batch ownership path; see [Ingestion modes](../ingestion-modes.md).

## Endpoint

`POST /api/v1/events/batch` accepts a JSON object with an `events` array of
canonical CloudEvents 1.0 structured-mode envelopes.

- A request contains from one to 100 events and is at most 1 MiB.
- Every event must have `specversion: "1.0"`, non-empty `id`, `type`,
  `source`, and `time`, and JSON `data`.
- Existing timestamp, resource identity, tenant identity, and OSAC v1
  extension validation applies to every member before any database write.
- The response is `204 No Content` only when all previously unseen events
  have been durably stored and processed. Invalid members return `400`; an
  identity collision returns `409`; neither case writes any member.

## Receipt and replay semantics

`raw_events` remains an append-only, non-unique audit log. It is not the
idempotency mechanism.

The receiver stores an `ingestion_receipts` row, unique on
`(event_source, event_id)`, containing a SHA-256 digest of the canonical
structured CloudEvent.

- A new receipt is claimed and the event is processed in the request
  transaction.
- A replay with the same source/id and digest is a successful no-op.
- The same source/id with a different digest is a `409 Conflict` and rolls
  back the whole request.
- Concurrent deliveries serialize on the receipt primary key; only one can
  produce inventory or billing effects.

## Transaction and processing model

All receipt claims, raw-event inserts, inventory changes, and event-driven
metering entries for a batch share one PostgreSQL transaction. The HTTP and
Kafka entry points use the same validation and event-processing function so
that supported OSAC v1 CloudEvents cannot be silently accepted by one path
and skipped by the other.

The legacy `POST /api/v1/events` endpoint remains for existing local callers;
it is not the adapter delivery protocol.

## Failure behavior

Database or processing failures return `500` and roll back the batch. The
adapter must treat timeouts, `429`, and `5xx` as unacknowledged delivery and
retry the unchanged batch; its Kafka runner must not commit offsets until a
`204` response.
