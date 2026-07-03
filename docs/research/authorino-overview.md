# Authorino — Overview

> Kubernetes-native authentication and authorization service used by the
> OSAC AI gateway. Part of the [Kuadrant](https://kuadrant.io/) project
> (Red Hat).
>
> Date: 2026-07-04

## What It Is

Authorino implements Envoy's external authorization gRPC protocol. Envoy
sends every incoming request to Authorino for an auth decision before
forwarding to the upstream service. Auth rules are defined declaratively
via `AuthConfig` Kubernetes CRDs — no code changes to the protected service.

**Repos:**
- [Kuadrant/authorino](https://github.com/Kuadrant/authorino) — the authorization service
- [Kuadrant/authorino-operator](https://github.com/Kuadrant/authorino-operator) — OLM operator for deployment
- [Architecture docs](https://github.com/Kuadrant/authorino/blob/main/docs/architecture.md)
- [User guides](https://github.com/Kuadrant/authorino/blob/main/docs/user-guides.md)

## Auth Pipeline

Authorino evaluates a 5-phase pipeline on every request:

| Phase | Purpose | Example |
|-------|---------|---------|
| **1. Authentication** | Validate the credential | JWT verification, API key lookup, K8s TokenReview, mTLS |
| **2. Metadata** | Fetch additional data from external sources | Call maas-api to resolve API key → username/groups/subscription |
| **3. Authorization** | Evaluate access policies | OPA, K8s RBAC (SubjectAccessReview), pattern matching, SpiceDB |
| **4. Response** | Inject headers or JSON into the Envoy response | Add `X-MaaS-Username`, `X-MaaS-Group` headers |
| **5. Callbacks** | Trigger webhooks on auth decisions | Audit logging, notifications |

All phases are optional except Authentication (at least one identity
source must resolve).

## Supported Authentication Methods

- **OpenID Connect** — validate JWTs using OIDC Discovery for automatic
  JWKS fetching
- **API keys** — stored in Kubernetes Secrets, looked up by Authorino
- **Kubernetes tokens** — validated via the TokenReview API
- **X.509 / mTLS** — verify client certificates against trusted CAs
- **Anonymous** — allow unauthenticated access (for public endpoints)

## How OSAC Uses Authorino

In the OSAC AI gateway, Authorino runs as the auth layer before the
IPP plugin chain:

```
Client (API key or K8s token)
  → Envoy Gateway
    → Authorino AuthPolicy:
        - API key path: POST /internal/v1/api-keys/validate → username, groups, subscription
        - K8s token path: TokenReview → username, groups; subscription from header
        - Injects: X-MaaS-Username, X-MaaS-Group, X-MaaS-Subscription
    → IPP plugins (metering, routing, translation...)
    → LLM Provider (Anthropic, OpenAI, vLLM...)
```

The [MaaSAuthPolicy controller](https://github.com/opendatahub-io/ai-gateway/blob/main/internal/controller/maasauthpolicy_controller.go)
generates Authorino `AuthConfig` CRDs that define this flow. Key details:

### API Key Authentication Path

1. Client sends `Authorization: Bearer sk-oai-...`
2. Authorino calls `POST /internal/v1/api-keys/validate` on maas-api
3. Response includes `username`, `groups`, `subscription`
4. Authorino injects these as `X-MaaS-*` headers via the Response phase

### K8s Token Authentication Path

1. Client sends `Authorization: Bearer <k8s-token>`
2. Authorino performs Kubernetes TokenReview
3. `username` and `groups` come from the TokenReview response
4. `subscription` comes from the client's `X-MaaS-Subscription` header
   (if provided)

### Header Injection (Response Phase)

Authorino uses CEL expressions to extract values from the auth context
and inject them as headers:

```
X-MaaS-Username:     auth.metadata.apiKeyValidation.username
                     OR auth.identity.user.username
X-MaaS-Group:        auth.metadata.apiKeyValidation.groups
                     OR auth.identity.user.groups
X-MaaS-Subscription: auth.metadata.apiKeyValidation.subscription
                     OR request.headers["x-maas-subscription"]
```

## Why It Matters to Us

Authorino is where the identity fields in our MaaS CloudEvents
**originate**. The chain is:

```
Authorino resolves identity
  → injects X-MaaS-* headers
    → maas-headers-guard captures to CycleState
      → external-metering reads from CycleState
        → CloudEvent data.user, data.group, data.subscription
          → our cost pipeline: tenant attribution
```

The `user`, `group`, and `subscription` fields we use for tenant
attribution are all resolved by Authorino at auth time. If we want a
`tenant_id` field added to the CloudEvent, the change would be in the
Authorino AuthPolicy configuration — adding a `X-MaaS-Tenant` header
derived from the subscription namespace. The data is already available
at that point.

See [maas-tenant-attribution.md](maas-tenant-attribution.md) for the
full tenant attribution analysis and implementation plan.

## Key Characteristics

- **Declarative** — all auth rules are K8s CRDs (`AuthConfig`)
- **Multi-tenant** — one Authorino instance serves multiple services
- **Zero-coding** — JWT, API key, mTLS, K8s tokens, OPA, RBAC out of the box
- **Envoy-native** — implements the standard `ext_authz` gRPC protocol
- **Extensible** — metadata phase can call arbitrary HTTP/gRPC services
  for data enrichment

## References

- [Authorino GitHub](https://github.com/Kuadrant/authorino)
- [Authorino Architecture](https://github.com/Kuadrant/authorino/blob/main/docs/architecture.md)
- [Authorino User Guides](https://github.com/Kuadrant/authorino/blob/main/docs/user-guides.md)
- [Kuadrant Auth Overview](https://docs.kuadrant.io/dev/kuadrant-operator/doc/overviews/auth/)
- [OSAC MaaSAuthPolicy Controller](https://github.com/opendatahub-io/ai-gateway/blob/main/internal/controller/maasauthpolicy_controller.go)
- [OSAC fulfillment-service — Authorino setup](https://github.com/osac-project/fulfillment-service/blob/main/docs/INSTALL.md)
