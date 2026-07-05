# ADR-004: Drop Unique Index on raw_events

**Date:** 2026-07-04
**Status:** Accepted

## Context

The `raw_events` table is an append-only audit log of every CloudEvent
received. It originally had a unique index on `event_id` used for
deduplication — `INSERT ... ON CONFLICT (event_id) DO NOTHING`.

CPU profiling of the ingest handler showed `InsertRawEvent` consuming
**33% of handler time** (140μs of 420μs cumulative). The unique index
check on every INSERT was the dominant cost. Dropping it and removing
the `ON CONFLICT` clause reduced MaaS event ingest latency by ~10%
(2,394μs → 2,167μs per event).

See: [profiling-results-2026-07-04.md](../research/profiling-results-2026-07-04.md)

## Decision

Remove the unique index on `raw_events.event_id`. Replace with a
regular (non-unique) index for query lookups. Remove `ON CONFLICT`
from the INSERT — all events are appended unconditionally.

Operators who want event-level dedup can create the unique index at
deployment time:
```sql
CREATE UNIQUE INDEX idx_raw_events_event_id ON raw_events (event_id);
```
This is a schema decision made at migration/deployment time, not a
runtime config.

## Rationale

1. **raw_events is a log, not a source of truth.** Nobody reads
   individual rows. The table is used only for:
   - Total count on the diagnostic dashboard
   - The nullable `raw_event_id` FK on `metering_entries` (never queried)

2. **Dedup that matters for billing is at the metering/cost level.**
   The rating sweep's `UnratedMeteringEntries` LEFT JOIN prevents
   duplicate cost entries. A duplicate raw event may produce duplicate
   metering entries, but the rating sweep handles this — each metering
   entry gets rated exactly once.

3. **The unique check is expensive at scale.** As the table grows
   (potentially millions of rows/day for MaaS), the btree index gets
   larger and the unique check per INSERT gets slower.

4. **Duplicate events are rare.** For MaaS (the high-volume path),
   events come from the IPP plugin with UUIDs — duplicates are
   effectively impossible. For capacity events from the Watch stream,
   replays on reconnect can produce duplicates, but these are harmless
   (extra metering entries for the same period just slightly inflate
   the total, corrected at the next reconciliation).

## Consequences

- **Positive:** ~10% ingest throughput improvement (measured). Simpler
  INSERT path. No `ON CONFLICT` logic in application code.
- **Negative:** Duplicate raw events are possible if the same event is
  sent twice. This does NOT cause duplicate billing (rating sweep
  prevents it), but the raw_events table may have duplicate rows.
- **Migration:** The `schemaEvolutions` block in `RunMigrations`
  automatically drops the unique index and creates a regular one on
  existing databases.

## Alternatives Considered

1. **Keep the unique index.** Rejected — 33% of handler time for a
   correctness guarantee that's already provided at the metering level.
2. **Skip raw_events entirely for MaaS.** Considered — would save the
   full INSERT cost (~33%), but loses the audit trail. May revisit if
   cross-event batching (T6) is implemented.
3. **Make dedup a runtime config flag.** Rejected — the index exists
   or doesn't at the schema level. A runtime flag would mean two code
   paths (`INSERT ... ON CONFLICT` vs plain `INSERT`), adding complexity
   for a decision that's made once per deployment.
