#!/usr/bin/env bash
# Build and push the cost-event-consumer multi-arch image to quay.io.
#
# Prerequisites:
#   - Docker Desktop running with the 'desktop-linux' buildx builder
#   - Logged in to quay.io (docker login quay.io)
#   - macOS keychain unlocked:
#       security -v unlock-keychain ~/Library/Keychains/login.keychain-db
#
# Usage:
#   ./scripts/build-push-consumer.sh              # latest tag
#   ./scripts/build-push-consumer.sh v1.2.3       # versioned tag

set -euo pipefail

IMAGE="quay.io/martin_povolny/cost-event-consumer"
TAG="${1:-latest}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONTEXT="$SCRIPT_DIR/../inventory-watcher"

echo "Building $IMAGE:$TAG (linux/amd64 + linux/arm64)..."
docker buildx build \
  --builder desktop-linux \
  --platform linux/amd64,linux/arm64 \
  -t "$IMAGE:$TAG" \
  -f "$CONTEXT/Containerfile" \
  --push \
  "$CONTEXT"

echo ""
echo "Pushed: $IMAGE:$TAG"
echo ""
echo "To redeploy on CRC:"
echo "  eval \$(crc oc-env)"
echo "  kubectl rollout restart deployment/cost-event-consumer -n cost-mgmt"
