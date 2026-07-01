# Open Questions for OSAC Team

> Consolidated from gap analyses and implementation work.
> Last updated: 2026-06-29

## Bare Metal (REQ-8)

1. **Hardware specs for BareMetalInstance** — The BareMetalInstance proto
   has no `cores`/`memory_gib` in its spec. It only references a
   `catalog_item`. To meter CPU/memory usage (like we do for VMs), we need
   to resolve the hardware profile from the catalog item → template chain.
   **Question:** Is this the intended lookup path? Or will hardware specs
   be added directly to BareMetalInstance in the future?

2. **Standalone bare metal** — Do we need to support bare metal nodes
   outside of an OpenShift cluster? The proto supports it but the
   requirement is unclear.

3. **BareMetalInstance in public Watch stream** — Currently only in the
   private event stream (field 27). Any plans to add it to the public
   oneOf? We can work around it via REST List polling, but real-time events
   would be better.

## Catalog Items

4. **Catalog item change frequency** — How often do catalog items change
   in practice? We plan to poll them via REST List periodically. Is
   5-minute polling sufficient, or should we request they be added to the
   public Watch stream?

5. **Catalog → pricing mapping** — The catalog item defines the SKU
   (template + field constraints). Should pricing/rates be associated with
   catalog items (SKU-based pricing) or with the underlying templates?
   This affects how we structure rate lookups.

## Private vs Public API

6. **Can the cost consumer use the private Watch stream?** — The private
   event stream has 28 entity types vs 10 on the public stream, including
   all catalog items, BareMetalInstance, networking entities, and a new
   `EVENT_TYPE_OBJECT_SIGNALED` event type. Since our consumer runs in the
   management cluster, is it authorized to use the private API?

## MaaS (REQ-2a / REQ-4)

7. **Who collects MaaS metrics — Cost or OSAC?** — RHOAI serves model
   inference. If OSAC collects from RHOAI and forwards via events, we're a
   pure consumer. If Cost must collect directly from RHOAI, we need a
   separate integration. Which is the plan?

8. **MaaS CloudEvents schema** — Our proposed schema (tokens_in,
   tokens_out, request_count, model_name, duration_seconds) is
   unconfirmed. Key unknowns:
   - Are token counts per-interval increments or cumulative?
   - Is `model_name` a stable identifier for rate lookups?
   - What states does a model deployment have?

9. **Will Model be an OSAC entity?** — If OSAC adds a Model entity to the
   fulfillment-service, it appears in the Watch stream and we handle it
   like VMs. If not, we need a different integration path.

10. **Token granularity** — The IPP external-metering plugin sends 4 token
    dimensions: prompt, completion, cached, reasoning. We currently meter
    `tokens_in` and `tokens_out`. Should we match the 4-dimension model,
    or map prompt→in, completion→out and add cached+reasoning as separate
    meters? Pricing may differ per dimension (e.g., cached tokens cheaper).

## Threshold Notifications (REQ-10)

10. **Does OSAC have an alerting/webhook endpoint?** — We implemented
    pull-based threshold checks (quota API returns crossed thresholds).
    For push notifications: does OSAC already have an alert ingestion
    endpoint, or do they need to build one?

11. **Alert transport** — Webhook with shared secret? CloudEvent POST?
    mTLS? What does the OSAC team prefer?

12. **Grace periods** — Does hitting 100% quota mean immediate cutoff or
    is there a grace window? This affects whether we send a single alert
    or a sequence (100% → grace started → grace expired).

## Event Transport

13. **Kafka for CloudEvents** — Is there a plan to deliver OSAC events
    via Kafka? Adding a Kafka consumer on our side is low effort (~150
    lines, same `handleEvent` pipeline). If OSAC plans Kafka delivery,
    we should align on topic naming, serialization format (JSON
    CloudEvents vs protobuf), and consumer group semantics.

14. **MaaS event delivery guarantees** — The IPP's `reportUsage` call
    (`POST /api/v1/events`) is currently fire-and-forget HTTP. For
    production, events cannot be lost. Options: Kafka, persistent local
    buffer with replay, or CloudEvents webhook with retry + DLQ. What
    infrastructure does OSAC already run? We'd rather build on that than
    introduce new components.

## Cluster Lifecycle (REQ-1a)

15. **"Cluster orders" vs Cluster entity** — The requirements mention
    "cluster orders" but OSAC has `Cluster` objects. Are these the same
    thing, or is there a separate cluster ordering workflow we should
    track?

## Tenant/Project Attribution (REQ-3a)

16. **Cost UI ownership** — Will providers view cost data in the Cost
    Management UI or in OSAC's own UI? This affects where we build
    reporting and who needs access.

17. **Quota scope** — Are quotas/budgets scoped per OSAC project or per
    tenant? Currently we scope quotas per tenant. If per-project is
    needed, we need project-level quota records.

18. **RBAC model** — For cross-project cost visibility, should we use
    Insights RBAC (one role per OSAC project — Koku-compatible) or
    Keycloak (OSAC-native)? Depends on whether this PoC merges into
    Koku or becomes a standalone replacement.
