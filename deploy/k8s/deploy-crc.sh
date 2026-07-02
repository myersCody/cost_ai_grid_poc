#!/bin/bash
set -e

# Configure oc environment
eval $(crc oc-env)

# Check connection
echo "Logged in as: $(oc whoami)"

# Create namespace (if not exists)
oc apply -f namespace.yaml

# Create secrets
oc create secret generic cost-consumer-secrets \
  --namespace=cost-mgmt \
  --from-literal=osac-token="dummy-token-for-now" \
  --dry-run=client -o yaml | oc apply -f -

# Deploy PostgreSQL
oc apply -f postgres.yaml

# Wait for PostgreSQL to be ready
echo "Waiting for PostgreSQL to be ready..."
oc wait --for=condition=ready pod -l app=cost-db -n cost-mgmt --timeout=120s

# Load image into CRC
echo "Loading image into CRC..."
docker save quay.io/cost-mgmt/cost-event-consumer:latest | eval $(crc podman-env) && podman load

# Deploy consumer
oc apply -f consumer.yaml

# Wait for deployment
echo "Waiting for consumer deployment..."
oc wait --for=condition=available deployment/cost-event-consumer -n cost-mgmt --timeout=120s

# Get status
echo ""
echo "=== Deployment Status ==="
oc get pods -n cost-mgmt

echo ""
echo "=== To access the dashboard ==="
echo "oc port-forward -n cost-mgmt svc/cost-event-consumer 8020:8020"
echo "open http://localhost:8020/debug/dashboard"
