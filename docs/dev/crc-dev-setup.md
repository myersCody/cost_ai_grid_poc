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
oc apply -f namespace.yaml
oc create secret generic cost-db-credentials \
  --namespace=cost-mgmt \
  --from-literal=user=costuser \
  --from-literal=password=costpass \
  --from-literal=connection-url="postgres://costuser:costpass@cost-db:5432/costdb?sslmode=disable"
oc create secret generic cost-consumer-secrets \
  --namespace=cost-mgmt \
  --from-literal=osac-token="dummy-token-for-now"
oc apply -f postgres.yaml
oc wait --for=condition=ready pod -l app=cost-db -n cost-mgmt --timeout=120s
oc apply -f consumer.yaml
oc wait --for=condition=available deployment/cost-event-consumer -n cost-mgmt --timeout=120s

# Verify
oc get pods -n cost-mgmt
oc logs -n cost-mgmt deployment/cost-event-consumer --tail=20

# Access (port-forward)
oc port-forward -n cost-mgmt svc/cost-event-consumer 8020:8020 &
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
oc rollout restart deployment/cost-event-consumer -n cost-mgmt

# 4. Watch rollout
oc rollout status deployment/cost-event-consumer -n cost-mgmt

# 5. Check logs
oc logs -n cost-mgmt deployment/cost-event-consumer -f
```

### Faster Iteration (skip push)

For faster iteration, you can tag images with a version and update the deployment:

```bash
# Build with version tag
docker build -t quay.io/martin_povolny/cost-event-consumer:dev-$(git rev-parse --short HEAD) -f Containerfile .
docker push quay.io/martin_povolny/cost-event-consumer:dev-$(git rev-parse --short HEAD)

# Update deployment
oc set image deployment/cost-event-consumer \
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
oc logs -n cost-mgmt deployment/cost-event-consumer --tail=10

# Query cost report
curl http://localhost:8020/api/v1/reports/costs?tenant_id=tenant-1 | jq
```

## Troubleshooting

### Pod Won't Start

```bash
# Check pod status
oc describe pod -n cost-mgmt -l app=cost-event-consumer

# Common issues:
# - ImagePullBackOff: Image not found on quay.io, check push succeeded
# - CrashLoopBackOff: Check logs for errors
# - Pending: Check PVC or resource constraints
```

### Database Connection Issues

```bash
# Check PostgreSQL is running
oc get pods -n cost-mgmt -l app=cost-db

# Connect to PostgreSQL pod
oc exec -it cost-db-0 -n cost-mgmt -- psql -U costuser -d costdb

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
oc create secret docker-registry quay-pull-secret \
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
oc get all -n cost-mgmt

# Check events for errors
oc get events -n cost-mgmt --sort-by='.lastTimestamp'
```

## Cleanup

### Delete Everything

```bash
# Delete all resources
oc delete namespace cost-mgmt

# Or delete individual components
oc delete -f consumer.yaml
oc delete -f postgres.yaml
oc delete secret cost-db-credentials cost-consumer-secrets -n cost-mgmt
```

### Stop CRC

```bash
# Stop CRC (keeps state)
crc stop

# Delete CRC (full cleanup)
crc delete
```

## Deploying OSAC Stack (Full Integration)

To test full integration with OSAC fulfillment-service, deploy the OSAC stack in CRC.

### Prerequisites (OSAC)

```bash
# Install operators
oc create -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml
oc create -f https://github.com/cert-manager/trust-manager/releases/download/v0.7.0/trust-manager.yaml

# Install Keycloak operator
oc create -f https://operatorhub.io/install/keycloak-operator.yaml

# Wait for operators to be ready
oc wait --for=condition=Available deployment/cert-manager -n cert-manager --timeout=300s
oc wait --for=condition=Available deployment/trust-manager -n cert-manager --timeout=300s
```

### Deploy OSAC

```bash
# Clone OSAC repo
git clone https://github.com/osac-project/fulfillment-service
cd fulfillment-service

# Create namespace
oc create namespace osac

# Deploy PostgreSQL for OSAC
oc apply -f - <<EOF
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: osac-db
  namespace: osac
spec:
  serviceName: osac-db
  replicas: 1
  selector:
    matchLabels:
      app: osac-db
  template:
    metadata:
      labels:
        app: osac-db
    spec:
      containers:
        - name: postgres
          image: quay.io/sclorg/postgresql-18-c10s:latest
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRESQL_USER
              value: user
            - name: POSTGRESQL_PASSWORD
              value: pass
            - name: POSTGRESQL_DATABASE
              value: db
          volumeMounts:
            - name: pgdata
              mountPath: /var/lib/pgsql/data
  volumeClaimTemplates:
    - metadata:
        name: pgdata
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 1Gi
---
apiVersion: v1
kind: Service
metadata:
  name: osac-db
  namespace: osac
spec:
  ports:
    - port: 5432
  selector:
    app: osac-db
EOF

# Deploy OSAC using Helm
helm install fulfillment-service ./charts/service/ \
  --namespace osac \
  --set variant=openshift \
  --set images.service=ghcr.io/osac-project/charts/fulfillment-service:main \
  --set postgresql.enabled=false \
  --set postgresql.host=osac-db \
  --set postgresql.database=db \
  --set postgresql.username=user \
  --set postgresql.password=pass

# Wait for OSAC to be ready
oc wait --for=condition=available deployment -l app.kubernetes.io/instance=fulfillment-service -n osac --timeout=300s
```

### Update Consumer to Use OSAC

```bash
# Generate OSAC token
cd /path/to/inventory-watcher
python3 scripts/gen_token.py > /tmp/osac_token_crc.txt

# Update consumer secret with real token
oc create secret generic cost-consumer-secrets \
  --namespace=cost-mgmt \
  --from-literal=osac-token="$(cat /tmp/osac_token_crc.txt)" \
  --dry-run=client -o yaml | oc apply -f -

# Update consumer deployment with OSAC service URL
oc set env deployment/cost-event-consumer \
  OSAC_BASE_URL=http://fulfillment-rest-gateway.osac.svc:8000 \
  -n cost-mgmt

# Restart consumer to pick up changes
oc rollout restart deployment/cost-event-consumer -n cost-mgmt
```

### Verify OSAC Integration

```bash
# Port-forward OSAC REST gateway
oc port-forward -n osac svc/fulfillment-rest-gateway 8011:8000 &

# Check OSAC API
curl -s http://localhost:8011/api/fulfillment/v1/clusters \
  -H "Authorization: Bearer $(cat /tmp/osac_token_crc.txt)" | jq

# Check consumer logs - should connect to OSAC successfully
oc logs -n cost-mgmt deployment/cost-event-consumer --tail=50 | grep -i osac
```

### Create Test Data in OSAC

Use the snippets from the local dev setup:

```bash
# Create test compute instances, clusters, etc.
cd /path/to/inventory-watcher
OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token_crc.txt) \
bash snippets/create-test-data.sh
```

## Differences from Local Dev

| Aspect | Local Dev | CRC Dev |
|--------|-----------|---------|
| **Databases** | Docker containers on localhost | StatefulSet in OpenShift |
| **Service** | Run as binary `./inventory-watcher` | Deployment in OpenShift |
| **OSAC** | Local gRPC + REST gateway binaries | Helm chart deployment with operators |
| **Networking** | localhost ports | K8s Services + port-forward |
| **Logs** | stdout | `oc logs` |
| **Config** | Environment variables | Secrets + ConfigMaps |
| **Iteration** | Fast (go run) | Slower (build → push → rollout) |
| **TLS** | Self-signed openssl cert | cert-manager managed |
| **Auth** | Local OIDC server script | Keycloak operator |

## Reference

- Full deployment plan: `docs/deployment-plan.md`
- Deployment manifests: `deploy/k8s/`
- Local dev setup: `docs/dev/local-dev-setup.md`
- OpenShift deployment summary: `docs/meeting-prep-2026-07-02.md`
