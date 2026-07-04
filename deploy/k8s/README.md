# CRC Deployment

## Prerequisites

- CRC running with sufficient resources (12GB+ RAM recommended)
- Docker logged into quay.io: `docker login quay.io`
- oc wrapper script: `./oc.sh`

## Quick Deploy

```bash
# 1. Build and push image
cd inventory-watcher
docker build -t quay.io/martin_povolny/cost-event-consumer:latest -f Containerfile .
docker push quay.io/martin_povolny/cost-event-consumer:latest

# 2. Deploy to CRC
cd ../deploy/k8s
bash ./oc.sh apply -f namespace.yaml
bash ./oc.sh create secret generic cost-db-credentials \
  --namespace=cost-mgmt \
  --from-literal=user=costuser \
  --from-literal=password=costpass \
  --from-literal=connection-url="postgres://costuser:costpass@cost-db:5432/costdb?sslmode=disable"
bash ./oc.sh create secret generic cost-consumer-secrets \
  --namespace=cost-mgmt \
  --from-literal=osac-token="dummy-token-for-now"
bash ./oc.sh apply -f postgres.yaml
bash ./oc.sh apply -f consumer.yaml

# 3. Wait for pods
bash ./oc.sh get pods -n cost-mgmt -w

# 4. Access dashboard
bash ./oc.sh port-forward -n cost-mgmt svc/cost-event-consumer 8020:8020
# Open http://localhost:8020/debug/dashboard
```

## Status

✅ PostgreSQL deployed and running
✅ cost-event-consumer deployed and running
✅ Health endpoints working (/healthz, /readyz)
✅ Dashboard accessible via port-forward
⏸️  OSAC not deployed (optional for testing ingest API directly)

## Test Endpoints

```bash
# Health checks
curl http://localhost:8020/healthz
curl http://localhost:8020/readyz

# Dashboard
open http://localhost:8020/debug/dashboard

# Cost report
curl http://localhost:8020/api/v1/reports/costs

# Send test event (MaaS)
curl -X POST http://localhost:8020/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{
    "specversion": "1.0",
    "type": "inference.tokens.used",
    "source": "test",
    "id": "test-1",
    "time": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
    "data": {
      "user": "tenant-1",
      "model": "llama-3",
      "prompt_tokens": 100,
      "completion_tokens": 50
    }
  }'
```
