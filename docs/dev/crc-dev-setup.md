# CRC Development Setup

Run the cost-event-consumer in CRC (CodeReady Containers) for OpenShift development and testing.

## Prerequisites

- **CRC** — [install guide](https://crc.dev/crc/getting_started/getting_started/installing/), then `crc config set memory 12288 cpus 4 && crc setup && crc start`
- **Docker** + **quay.io account** — `docker login quay.io`

## Quick Start

```bash
# Start CRC and configure oc
crc start
eval $(crc oc-env)
oc login -u kubeadmin
# Or use wrapper: bash oc.sh whoami

# Build and push image

cd inventory-watcher
docker build -t quay.io/martin_povolny/cost-event-consumer:latest -f Containerfile .
docker push quay.io/martin_povolny/cost-event-consumer:latest

# Deploy stack
cd ../deploy/k8s
bash ../../oc.sh apply -f namespace.yaml
bash ../../oc.sh create secret generic cost-db-credentials \
  --namespace=cost-mgmt \
  --from-literal=user=costuser \
  --from-literal=password=costpass \
  --from-literal=connection-url="postgres://costuser:costpass@cost-db:5432/costdb?sslmode=disable"
bash ../../oc.sh create secret generic cost-consumer-secrets \
  --namespace=cost-mgmt \
  --from-literal=osac-token="dummy-token-for-now"
bash ../../oc.sh apply -f postgres.yaml
bash ../../oc.sh wait --for=condition=ready pod -l app=cost-db -n cost-mgmt --timeout=120s
bash ../../oc.sh apply -f consumer.yaml
bash ../../oc.sh wait --for=condition=available deployment/cost-event-consumer -n cost-mgmt --timeout=120s

# Verify
bash ../../oc.sh get pods -n cost-mgmt
bash ../../oc.sh logs -n cost-mgmt deployment/cost-event-consumer --tail=20

# Access (port-forward)
bash ../../oc.sh port-forward -n cost-mgmt svc/cost-event-consumer 8020:8020 &
curl http://localhost:8020/healthz
open http://localhost:8020/debug/dashboard
```

## Development Workflow

### Code → Build → Deploy Cycle

```bash
# 1. Make code changes in inventory-watcher/

# 2. Rebuild image
cd inventory-watcher
docker build -t quay.io/martin_povolny/cost-event-consumer:latest -f Containerfile .
docker push quay.io/martin_povolny/cost-event-consumer:latest

# 3. Force pod restart to pull new image
bash ../oc.sh rollout restart deployment/cost-event-consumer -n cost-mgmt

# 4. Watch rollout
bash ../oc.sh rollout status deployment/cost-event-consumer -n cost-mgmt

# 5. Check logs
bash ../oc.sh logs -n cost-mgmt deployment/cost-event-consumer -f
```

### Faster Iteration (skip push)

For faster iteration, you can tag images with a version and update the deployment:

```bash
# Build with version tag
docker build -t quay.io/martin_povolny/cost-event-consumer:dev-$(git rev-parse --short HEAD) -f Containerfile .
docker push quay.io/martin_povolny/cost-event-consumer:dev-$(git rev-parse --short HEAD)

# Update deployment
bash ../oc.sh set image deployment/cost-event-consumer \
  consumer=quay.io/martin_povolny/cost-event-consumer:dev-$(git rev-parse --short HEAD) \
  -n cost-mgmt
```

### Testing Event Ingestion

```bash
# Send a test MaaS event
curl -X POST http://localhost:8020/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{
    "specversion": "1.0",
    "type": "inference.tokens.used",
    "source": "test",
    "id": "test-'$(date +%s)'",
    "time": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
    "data": {
      "user": "tenant-1",
      "model": "llama-3",
      "prompt_tokens": 100,
      "completion_tokens": 50,
      "total_tokens": 150
    }
  }'

# Check the event was processed
bash ../oc.sh logs -n cost-mgmt deployment/cost-event-consumer --tail=10

# Query cost report
curl http://localhost:8020/api/v1/reports/costs?tenant_id=tenant-1 | jq
```

## Troubleshooting

### Pod Won't Start

```bash
# Check pod status
bash ../../oc.sh describe pod -n cost-mgmt -l app=cost-event-consumer

# Common issues:
# - ImagePullBackOff: Image not found on quay.io, check push succeeded
# - CrashLoopBackOff: Check logs for errors
# - Pending: Check PVC or resource constraints
```

### Database Connection Issues

```bash
# Check PostgreSQL is running
bash ../../oc.sh get pods -n cost-mgmt -l app=cost-db

# Connect to PostgreSQL pod
bash ../../oc.sh exec -it cost-db-0 -n cost-mgmt -- psql -U costuser -d costdb

# Check tables were created
\dt

# Expected tables:
# raw_events, compute_instances, clusters, models, metering_entries,
# cost_entries, quotas, alerts, rates, etc.
```

### Image Pull Errors

If the image won't pull from quay.io:

```bash
# Make sure the repository is public
# Or create image pull secret for private repos:
bash ../../oc.sh create secret docker-registry quay-pull-secret \
  --docker-server=quay.io \
  --docker-username=<your-username> \
  --docker-password=<your-password> \
  -n cost-mgmt

# Add to deployment (edit consumer.yaml):
# spec:
#   template:
#     spec:
#       imagePullSecrets:
#         - name: quay-pull-secret
```

### View All Resources

```bash
# Everything in cost-mgmt namespace
bash ../../oc.sh get all -n cost-mgmt

# Check events for errors
bash ../../oc.sh get events -n cost-mgmt --sort-by='.lastTimestamp'
```

## Cleanup

### Delete Everything

```bash
# Delete all resources
bash ../../oc.sh delete namespace cost-mgmt

# Or delete individual components
bash ../../oc.sh delete -f consumer.yaml
bash ../../oc.sh delete -f postgres.yaml
bash ../../oc.sh delete secret cost-db-credentials cost-consumer-secrets -n cost-mgmt
```

### Stop CRC

```bash
# Stop CRC (keeps state)
crc stop

# Delete CRC (full cleanup)
crc delete
```

## Differences from Local Dev

| Aspect | Local Dev | CRC Dev |
|--------|-----------|---------|
| **Databases** | Docker containers on localhost | StatefulSet in OpenShift |
| **Service** | Run as binary `./inventory-watcher` | Deployment in OpenShift |
| **OSAC** | Local gRPC + REST gateway | Not deployed yet (Phase 5) |
| **Networking** | localhost ports | K8s Services + port-forward |
| **Logs** | stdout | `oc logs` |
| **Config** | Environment variables | Secrets + ConfigMaps |
| **Iteration** | Fast (go run) | Slower (build → push → rollout) |

## Next Steps

- **Deploy OSAC in CRC:** Follow Phase 5 in `docs/deployment-plan.md` to deploy the full OSAC fulfillment-service stack with cert-manager and Keycloak
- **Add ServiceMonitor:** For Prometheus metrics scraping
- **Create Helm Chart:** Extract manifests into a proper Helm chart for production
- **CI/CD:** Set up automated image builds on PR/merge

## Reference

- Full deployment plan: `docs/deployment-plan.md`
- Deployment manifests: `deploy/k8s/`
- Local dev setup: `docs/dev/local-dev-setup.md`
- OpenShift deployment summary: `docs/meeting-prep-2026-07-02.md`
