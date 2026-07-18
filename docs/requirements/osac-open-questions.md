# Open Questions for OSAC Team

> Consolidated from gap analyses and implementation work.
> Last updated: 2026-07-14

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
   > **Update (Jul 14, 2026 meeting):** Martin confirmed with Aishay that
   > bare metal events are indeed missing from the public gRPC API
   > (private-only). Path forward: Martin will either file a PR upstream
   > himself or work with Aishay to find the right owner to add it to the
   > public stream. No committed timeline yet.

## Catalog Items

4. **Catalog item change frequency** — How often do catalog items change
   in practice? We plan to poll them via REST List periodically. Is
   5-minute polling sufficient, or should we request they be added to the
   public Watch stream?
   > **Update (Jul 14, 2026 meeting):** Martin noted catalog items are
   > also missing from the public gRPC stream (private-only, same as bare
   > metal — see item 3 and item 6). However, unlike bare metal, catalog
   > items don't need real-time delivery for the PoC — periodic REST List
   > polling is confirmed sufficient. Getting catalog items onto the
   > public stream is a nice-to-have, not a blocker.

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
   > **Update (Jul 14, 2026 meeting):** Per Martin's conversation with
   > Aishay, the recommendation is to use the **public** gRPC API, not the
   > private one — the public API is simply missing some entities today
   > (bare metal, catalog items). Martin will pursue getting those added
   > to the public stream (item 3, item 4) rather than relying on private
   > API access.

## MaaS (REQ-2a / REQ-4)

7. **Who collects MaaS metrics — Cost or OSAC?** — RHOAI serves model
   inference. If OSAC collects from RHOAI and forwards via events, we're a
   pure consumer. If Cost must collect directly from RHOAI, we need a
   separate integration. Which is the plan?
   > **Update (Jul 14, 2026 meeting):** Moti is designing an OSAC-side
   > metering service that would collect all events for the system and
   > expose a single entry point, with adapters to each downstream system
   > (Cost Management, M360, OpenMeter, etc.) — meaning Cost would not
   > need to interact with MaaS directly. This is still a draft design,
   > not yet reviewed, and Moti is **not confident it lands by end of
   > July**. Decision for the PoC: keep building the current real-time
   > direct-integration path (Martin/Noy touch points) in parallel; if
   > OSAC's collector is ready in time, integrate with it, otherwise
   > ship with the direct path and revisit Aug 1. Not fully resolved —
   > still need OSAC to confirm final direction.

8. **MaaS CloudEvents schema** — Our proposed schema (tokens_in,
   tokens_out, request_count, model_name, duration_seconds) is
   unconfirmed. Key unknowns:
   - Are token counts per-interval increments or cumulative?
   - Is `model_name` a stable identifier for rate lookups?
   - What states does a model deployment have?

9. **Will Model be an OSAC entity?** — It is unclear whether OSAC will
   add a formal Model entity to the fulfillment-service (proto + API +
   Watch stream, like ComputeInstance) or whether models will remain
   identified only by name in the CloudEvent `data.model` field. Our
   implementation works either way, but the answer affects inventory
   tracking and reconciliation. If Model becomes an entity, we add it
   to the watcher/reconciler. If not, CloudEvents ingest is the only
   data source for MaaS.

10. **Token granularity** — The IPP external-metering plugin sends 4 token
    dimensions: prompt, completion, cached, reasoning. We currently meter
    `tokens_in` and `tokens_out`. Should we match the 4-dimension model,
    or map prompt→in, completion→out and add cached+reasoning as separate
    meters? Pricing may differ per dimension (e.g., cached tokens cheaper).
    > **Update (Jul 18, 2026):** Decision made — only `maas_tokens_in`, `maas_tokens_out`, and `maas_requests` are metered. `cached_input_tokens` and `reasoning_tokens` are subsets of input/output tokens (per OpenAI API spec); billing them separately would double-count. They are parsed from CloudEvents for observability but do not produce metering entries.

## Threshold Notifications (REQ-10)

11. **Quota alert mechanism shelved** — Per "Cost Management + OSAC"
    meeting, 2026-07-02: "quota alert mechanism — deferred until after
    the PoC." Pull-based threshold checks (quota API + IPP balance check)
    are the agreed approach for now. Push webhooks not needed for PoC.
    Revisit post-PoC if OSAC builds an alert ingestion endpoint.
    > **Update (Jul 14, 2026 meeting):** Ronnie drew a distinction that
    > refines this: **budget quotas** (monetary) could plausibly be
    > handled with the event/notification model, since checking budget
    > before starting a resource and alerting at 80%/90% doesn't need
    > sub-second consistency. **Usage quotas** (VM count, storage, etc.)
    > are different — OSAC needs a synchronous answer *during* the
    > provisioning flow (e.g. "can I start this VM right now"), so an
    > event-based/eventually-consistent model risks OSAC duplicating
    > state it would otherwise get from Cost. Conclusion: both a
    > pre-check API (REQ-9, have it) and a push alert mechanism are
    > needed, they're just not interchangeable. Separately, Moti
    > reiterated OSAC has **no receiver** to act on any notification
    > event today — even if Cost emits it, nothing on OSAC's side
    > consumes or acts on it (audit-only value at best). Martin noted
    > this is cheap for Cost to add on short notice ("by the end of the
    > week or sooner") **if** OSAC hands over a concrete CloudEvent spec
    > for what they want to receive — the ball is in OSAC's court to
    > define the receiver and the schema.

11. ~~**Alert transport**~~ — Deferred per above. If revisited post-PoC:
    webhook with shared secret, CloudEvent POST, or mTLS.

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

15. ~~**"Cluster orders" vs Cluster entity**~~ — **Resolved.** ClusterOrder
    is the ordering/provisioning workflow: a user POSTs a ClusterOrder, the
    operator provisions the actual Cluster, and the order transitions
    through states (`CLUSTER_ORDER_STATE_ACCEPTED` → `_FULFILLED` /
    `_FAILED`). The resulting Cluster is a separate entity in the Watch
    stream. For cost purposes we track the **Cluster** (the running
    resource that incurs cost), not the ClusterOrder (the purchase request).
    No action needed — we already consume Cluster events.
    *Source: wg-osac-eng Slack thread, 2025-04-02, Juan Antonio Hernandez
    Fernandez's test workflow showing the two-step create-cluster →
    update-order-status flow.*

## Tenant/Project Attribution (REQ-3a)

16. **Cost UI ownership** — Will providers view cost data in the Cost
    Management UI or in OSAC's own UI? This affects where we build
    reporting and who needs access.
    > **Update (Jul 14, 2026 meeting):** Discussion expanded this beyond
    > "where is cost shown." OSAC has multiple UI surfaces (provider
    > portal, tenant portal, user portal), and the experience will differ
    > depending on which billing backend a customer uses (M360 vs
    > OpenMeter vs Cost Management vs a third party) — Pau's view is that
    > using Cost Management's own tooling should feel as integrated as
    > using native OSAC/ACM/OpenShift tooling, similar to how not using
    > native permissions in OpenShift/ACM is expected to feel less
    > integrated. Moti and Pau converged on needing an **abstraction
    > layer** — not just for metering, but for catalog and UI too — e.g.
    > OSAC exposes generic placeholder screens/APIs that any billing
    > backend (Cost Management, M360, OpenMeter, third party) can push
    > data into, so the experience is consistent regardless of which
    > backend is source of truth. Still unresolved whether this is
    > feasible for Jul 31 or Aug 31; if not, action item is to track it
    > as future work.

17. ~~**Quota scope**~~ — **Resolved.** Quotas/budgets scoped per tenant
    AND per project. Projects roll up to tenant (sum of project
    consumptions cannot exceed tenant quota). Overcommitting at project
    level is allowed. Currently we only have tenant-level quotas;
    project-level quotas with rollup is new work.
    *Source: PR #33 (Pau Garcia Quiles), REQ-3a acceptance criteria.*

18. **RBAC model** — Pau clarified (PR #33): "Using Insights RBAC is
    NOT mandatory. We may want to move to a simpler model, e.g. per
    tenant and project, like OSAC does, where authentication is what
    matters and authorization hardly exists." Fine-grained RBAC deferred
    post-PoC provided Cost implements project-within-tenant concept.
    Still open: final decision on Insights RBAC vs Keycloak-native.

## MaaS Tenant Attribution (IPP Integration)

19. **How to derive tenant from IPP CloudEvent?** — The IPP CloudEvent
    ([plugin.go](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/plugin.go))
    carries `user`, `group`, `subscription` but no `tenant_id`. The
    MaaSSubscription CR is namespaced
    ([e2e report](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/main/test/e2e/reports/3.4/external-model-e2e-report.md))
    but may live in a shared MaaS namespace, not per-tenant. Which of
    these fields maps to the OSAC tenant? Or do we need an explicit
    `tenant_id` added to the event?
    See [research](../research/maas-tenant-attribution.md).

20. **Add X-MaaS-Tenant upstream?** — Would it be feasible to add a
    `X-MaaS-Tenant` header in the Authorino AuthPolicy (from the
    subscription namespace) and a `tenant_id` field in the CloudEvent
    data payload? This would give us clean attribution without
    convention-based parsing.

## Data Privacy / PII (MaaS)

21. **User-level attribution and PII** — Raised by Avishay/Yon's design
    review: if OSAC/RHOAI forwards per-user identifiers (`user_id`,
    `subscription_id`) in MaaS CloudEvents, Cost Management could
    identify which individual user consumed how much (e.g. "$1,000 by
    user X"). Is this considered sensitive personal information?
    **Discussion (Jul 14, 2026 meeting):** Action Item: Pau to investigate.
    **Not resolved** — no answer yet on whether per-user cost attribution
    (REQ-3) needs to be restricted, anonymized, or aggregated to avoid
    exposing individual user consumption.

## Catalog Pricing Model

22. **Catalog price override by tenant** — Can a tenant (or tenant admin)
    override the prices of catalog items set by the CSP/provider? Or can
    a tenant admin create their own offering to their own tenant's users
    (e.g. a curated image bundled with software, priced with a markup)?
    Raised by Moti as an open question with no answer yet — it's unclear
    whether OSAC plans to support either capability. Two distinct
    behaviors are bundled in this question: (a) different catalog prices
    per tenant, and (b) a tenant admin creating and pricing their own
    sub-offerings for their users. Neither has been discussed
    conclusively; Rafi's training material covers user experience but not
    the administration/pricing model. Relates to item 5 (catalog →
    pricing mapping).

23. **ComputeInstance dropping CPU/memory fields** — Moti flagged an
    upcoming OSAC change where CPU and memory are being removed from
    `ComputeInstance`, and the measured/billable unit becomes
    `instance_type` only. If Cost Management's cost model doesn't rely on
    raw CPU/memory (which it shouldn't, per REQ-3b's catalog-item-based
    pricing design), this should be a non-issue — but it needs explicit
    verification. Action item: Martin to confirm cost calculation works
    purely from instance type and doesn't break when CPU/memory fields
    disappear. Martin asked Moti for a pointer to the relevant PR/
    discussion to track this.
    > **Update (Jul 18, 2026):** Catalog fallback implemented in the metering sweep (PR #59) — when `cores == 0`, specs resolved from InstanceType catalog. Per-SKU pricing via `instance_type` rate dimension also implemented. See [req3b gap analysis](req3b-instance-type-only-gap-analysis.md).
