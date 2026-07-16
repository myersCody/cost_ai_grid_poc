# k3s Full-Stack Demo: OSAC + IPP MaaS Gateway

Deploy the complete cost management demo on a local k3s cluster with
**both** event paths running simultaneously:

1. **OSAC path** (capacity) — real OSAC fulfillment-service producing
   VM, cluster, and bare-metal lifecycle events via the Watch stream
2. **IPP/MaaS path** (consumption) — Istio gateway with IPP ext_proc
   plugin and llm-katan as a mock LLM backend, producing real
   inference metering CloudEvents

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        k3s cluster                                  │
│                                                                     │
│  ┌──────────── namespace: osac ──────────────────────────────────┐  │
│  │  osac-db (PostgreSQL)                                         │  │
│  │  osac-oidc (mock OIDC issuer)                                 │  │
│  │  osac-grpc (fulfillment gRPC server)                          │  │
│  │  osac-rest (REST gateway → gRPC)                              │  │
│  └───────────────────────────────────┬───────────────────────────┘  │
│                                      │ Watch stream + List          │
│                                      ▼                              │
│  ┌──────────── namespace: cost-mgmt ────────────────────────────┐  │
│  │                                                               │  │
│  │  cost-db (PostgreSQL)                                         │  │
│  │  cost-event-consumer ◄────── CloudEvents (MaaS) ──┐          │  │
│  │    ├─ watcher        (OSAC Watch stream)           │          │  │
│  │    ├─ reconciler     (OSAC List, periodic)         │          │  │
│  │    ├─ metering       (60s sweep)                   │          │  │
│  │    ├─ rating         (30s sweep)                   │          │  │
│  │    └─ ingest HTTP    (POST /api/v1/events)         │          │  │
│  │              ▲                                     │          │  │
│  │              │ balance check                       │          │  │
│  └──────────────┼─────────────────────────────────────┘          │  │
│                 │                                                   │
│  ┌──────────── namespace: ai-gateway (Istio mesh) ──────────────┐  │
│  │                                                               │  │
│  │  Istio Gateway (Envoy + ext_proc) ──► IPP Payload Processor  │  │
│  │       │                                    │                  │  │
│  │       ▼                                    │ metering events  │  │
│  │  llm-katan (echo-mode mock LLM)            │ ─────────────►  │  │
│  │                                                               │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────── namespace: monitoring (optional) ────────────────┐  │
│  │  Prometheus ──scrape──► cost-consumer:9000/metrics            │  │
│  │  Grafana    ──query──► Prometheus                             │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────── namespace: istio-system ─────────────────────────┐  │
│  │  istiod (ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true)         │  │
│  └──────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘

External:
  hey / curl ──► NodePort :30080 ──► Istio Gateway ──► llm-katan
                                         │
                                         ▼
                            IPP external-metering plugin
                                         │
                            POST /api/v1/events ──► consumer
```

## Prerequisites

- **k3s** — `curl -sfL https://get.k3s.io | sh -` (or k3d for Docker-based)
- **Helm** — for IPP chart deployment
- **Docker** — to build container images (imported to k3s via `ctr`)
- **kubectl** — configured for the k3s cluster

### Building the OSAC image

The OSAC fulfillment-service image is not public. To build it:

```bash
git clone https://github.com/osac-project/fulfillment-service /tmp/fulfillment-service
cd /tmp/fulfillment-service
go build -o fulfillment-service ./cmd/fulfillment-service/
docker build -t osac-fulfillment-service:ci .

# Generate TLS certs for OIDC mock
openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt \
    -days 365 -nodes -subj '/CN=osac-oidc' \
    -addext 'subjectAltName=DNS:osac-oidc,DNS:osac-oidc.osac.svc'
```

## Quick Start

```bash
# Full stack (OSAC + IPP + monitoring)
./snippets/k3s-full-stack.sh up

# Tear down
./snippets/k3s-full-stack.sh down

# Just IPP path (no OSAC, lighter setup)
./snippets/k3s-full-stack.sh up --ipp-only

# Status check
./snippets/k3s-full-stack.sh status
```

## Event Paths

### Path 1: OSAC (Capacity-Based)

Real VM/cluster lifecycle events flow through the OSAC Watch stream:

```
OSAC fulfillment-service
  → gRPC Watch stream
  → cost-event-consumer watcher goroutine
  → raw_events table
  → metering (60s sweep: compute uptime hours)
  → rating (30s sweep: tiered pricing)
  → cost_entries (Infrastructure / Supplementary)
```

The reconciler also does periodic List calls to catch any missed events.

Resource types: `compute_instance`, `cluster`, `baremetal_instance`

### Path 2: IPP/MaaS (Consumption-Based)

Inference requests flow through the Istio gateway with IPP metering:

```
curl -X POST /v1/chat/completions
  → Istio Gateway (Envoy)
  → IPP ext_proc (external processing filter)
    → ① balance check: GET consumer/api/v1/customers/{id}/entitlements/...
    → forward request to llm-katan
    → llm-katan responds with usage{} block
    → ② report usage: POST consumer/api/v1/events (CloudEvent)
  → response returned to caller

CloudEvent at consumer:
  → raw_events table
  → metering (maas_tokens_in, _out, _cached, _reasoning)
  → rating (per-token pricing)
  → cost_entries
```

Resource type: `model`

### Sending Test Requests

Once deployed, send inference requests through the gateway:

```bash
# Through the Istio gateway (full path)
curl http://localhost:30080/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "x-maas-username: demo-user" \
    -H "x-maas-group: demo-tenant" \
    -H "x-maas-subscription: demo-tenant/premium" \
    -d '{
        "model": "test-model",
        "messages": [{"role": "user", "content": "Hello from the demo!"}]
    }'

# Continuous load (10 req/s for 60 seconds)
hey -n 600 -c 10 -m POST \
    -H "Content-Type: application/json" \
    -H "x-maas-username: load-user" \
    -H "x-maas-group: load-tenant" \
    -H "x-maas-subscription: load-tenant/standard" \
    -D '{"model":"test-model","messages":[{"role":"user","content":"load test"}]}' \
    http://localhost:30080/v1/chat/completions
```

### Verifying Data Flow

```bash
# Check consumer health + counts
curl -s http://localhost:8020/api/v1/reports/summary | jq .

# Check cost report (grouped by resource type)
curl -s 'http://localhost:8020/api/v1/reports/costs?group_by=resource_type' | jq .

# Check Prometheus metrics
curl -s http://localhost:9000/metrics | grep cost_consumer

# Open the debug dashboard
open http://localhost:8020/debug/dashboard
```

## Port Forwards (set up by the script)

| Port  | Service                      | Namespace  |
|-------|------------------------------|------------|
| 8020  | cost-event-consumer (API)    | cost-mgmt  |
| 9000  | cost-event-consumer (metrics)| cost-mgmt  |
| 30080 | Istio Gateway (NodePort)     | ai-gateway |
| 8011  | OSAC REST gateway            | osac       |
| 3000  | Grafana (if monitoring)      | monitoring |

## Differences from CI

The CI workflow (`.github/workflows/ipp-integration.yml`) runs on
ephemeral GitHub Actions runners. This local script differs:

- Uses k3s directly (not k3d) — same binary, no Docker layer
- Exposes services via NodePort + port-forward (CI uses pod exec)
- Includes optional monitoring stack (Prometheus + Grafana)
- Includes OSAC stack (CI tests OSAC and IPP separately)
- Designed for interactive demo, not CI pass/fail

## Troubleshooting

```bash
# All pods across namespaces
kubectl get pods -A

# Consumer logs
kubectl logs -n cost-mgmt deployment/cost-event-consumer -f

# IPP logs (look for metering plugin errors)
kubectl logs -n ai-gateway deployment/payload-processing -f

# llm-katan logs
kubectl logs -n ai-gateway deployment/llm-katan -f

# OSAC gRPC server logs
kubectl logs -n osac deployment/osac-grpc -f

# Gateway status
kubectl get gateway -n ai-gateway -o yaml

# Check EnvoyFilter applied
kubectl get envoyfilter -n ai-gateway -o yaml
```
