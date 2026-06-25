#!/bin/bash
# Create test data in a local OSAC fulfillment-service instance.
# Requires: OSAC REST gateway running on localhost:8011, valid token in /tmp/osac_token.txt
set -euo pipefail

TOKEN=$(cat /tmp/osac_token.txt)
BASE=http://localhost:8011

echo "=== Creating instance types ==="
for spec in "standard-2-8:2:8" "standard-4-16:4:16" "standard-8-32:8:32"; do
  IFS=: read -r name cores mem <<< "$spec"
  curl -s -X POST "$BASE/api/fulfillment/v1/instance_types" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d "{
      \"metadata\": {\"name\": \"$name\"},
      \"spec\": {\"cores\": $cores, \"memory_gib\": $mem, \"description\": \"$cores cores, ${mem}GB\", \"state\": \"INSTANCE_TYPE_STATE_ACTIVE\"}
    }" | jq -c '{id: .id, name: .metadata.name}' || true
done

echo ""
echo "=== Creating network class (private API) ==="
NC_RESP=$(curl -s -X POST "$BASE/api/private/v1/network_classes" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
  -d '{
    "metadata": {"name": "default-nc"},
    "title": "Default Network",
    "description": "Default test network class",
    "implementation_strategy": "ovn-kubernetes",
    "is_default": true
  }')
NC_ID=$(echo "$NC_RESP" | jq -r '.id')
echo "Network class: $NC_ID"

echo ""
echo "=== Creating virtual network ==="
VN_RESP=$(curl -s -X POST "$BASE/api/fulfillment/v1/virtual_networks" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
  -d '{"metadata": {"name": "test-vnet"}, "spec": {"ipv4_cidr": "10.0.0.0/16"}}')
VN_ID=$(echo "$VN_RESP" | jq -r '.id')
echo "Virtual network: $VN_ID"

# Set VN to READY via private API
curl -s -X PATCH "$BASE/api/private/v1/virtual_networks/$VN_ID" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
  -d "{\"id\": \"$VN_ID\", \"spec\": {\"ipv4_cidr\": \"10.0.0.0/16\", \"region\": \"default\", \"network_class\": \"$NC_ID\"}, \"status\": {\"state\": \"VIRTUAL_NETWORK_STATE_READY\"}}" > /dev/null

echo ""
echo "=== Creating subnet (private API) ==="
SUBNET_RESP=$(curl -s -X POST "$BASE/api/private/v1/subnets" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
  -d "{\"metadata\": {\"name\": \"test-subnet\"}, \"spec\": {\"virtual_network\": \"$VN_ID\", \"ipv4_cidr\": \"10.0.1.0/24\"}, \"status\": {\"state\": \"SUBNET_STATE_READY\"}}")
SUBNET_ID=$(echo "$SUBNET_RESP" | jq -r '.id')
echo "Subnet: $SUBNET_ID"

echo ""
echo "=== Creating compute instance template (private API) ==="
TPL_RESP=$(curl -s -X POST "$BASE/api/private/v1/compute_instance_templates" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
  -d '{"metadata": {"name": "basic-vm"}, "title": "Basic VM", "description": "Basic virtual machine template"}')
TPL_ID=$(echo "$TPL_RESP" | jq -r '.id')
echo "Template: $TPL_ID"

echo ""
echo "=== Creating compute instances (private API) ==="
for spec in "worker-1:4:16:test:worker" "worker-2:8:32:test:gpu-worker" "worker-3:12:48:production:api-server"; do
  IFS=: read -r name cores mem env role <<< "$spec"
  RESP=$(curl -s -X POST "$BASE/api/private/v1/compute_instances" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d "{
      \"metadata\": {\"name\": \"$name\", \"labels\": {\"env\": \"$env\", \"role\": \"$role\"}},
      \"spec\": {
        \"template\": \"$TPL_ID\",
        \"cores\": $cores, \"memory_gib\": $mem,
        \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
        \"boot_disk\": {\"size_gib\": 100},
        \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
        \"run_strategy\": \"Always\"
      },
      \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\"}
    }")
  echo "$name: $(echo "$RESP" | jq -c '{id: .id, cores: .spec.cores, mem: .spec.memory_gib}')"
done

echo ""
echo "=== Done ==="
echo "Verify: curl -s $BASE/api/fulfillment/v1/compute_instances -H 'Authorization: Bearer \$(cat /tmp/osac_token.txt)' | jq '.size'"
