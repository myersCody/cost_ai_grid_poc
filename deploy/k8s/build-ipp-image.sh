#!/usr/bin/env bash
# Build and push a custom IPP (ai-gateway-payload-processing) image to quay.io.
#
# Usage:
#   bash deploy/k8s/build-ipp-image.sh [BRANCH] [IMAGE_TAG]
#
# Defaults:
#   BRANCH     — branch already checked out in IPP_REPO_DIR (skips checkout)
#   IMAGE_TAG  — "latest"
#
# Examples:
#   bash deploy/k8s/build-ipp-image.sh                              # build current checkout
#   bash deploy/k8s/build-ipp-image.sh feat/tenant-attribution-fields pr-386
#
# Prerequisites:
#   - docker login quay.io
#   - Go toolchain (GOTOOLCHAIN=auto will download Go 1.25 if needed)
#   - Quay repo exists: quay.io/martin_povolny/ipp-metering
#   - IPP repo cloned at IPP_REPO_DIR (or will be cloned automatically)

set -euo pipefail

QUAY_IMAGE="quay.io/martin_povolny/ipp-metering"
IPP_REPO_DIR="${IPP_REPO_DIR:-${HOME}/Projects/koku/ai-gateway-payload-processing}"
IPP_UPSTREAM="https://github.com/martinpovolny/ai-gateway-payload-processing"
BRANCH="${1:-}"
IMAGE_TAG="${2:-latest}"
BINARY="/tmp/ipp-bbr"

# ── Clone if needed ──
if [ ! -d "$IPP_REPO_DIR/.git" ]; then
  echo "--- Cloning IPP repo ---"
  git clone "$IPP_UPSTREAM" "$IPP_REPO_DIR"
fi

cd "$IPP_REPO_DIR"

# ── Checkout branch if specified ──
if [ -n "$BRANCH" ]; then
  echo "--- Checking out branch: ${BRANCH} ---"
  git fetch origin
  git checkout "$BRANCH"
fi

echo "--- Building binary (branch: $(git rev-parse --abbrev-ref HEAD), commit: $(git rev-parse --short HEAD)) ---"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=auto go build -o "$BINARY" ./cmd

echo "--- Building image ---"
docker build \
  -t "${QUAY_IMAGE}:${IMAGE_TAG}" \
  -f - /tmp <<'EOF'
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY ipp-bbr /bbr
USER 1001
ENTRYPOINT ["/bbr"]
EOF

echo "--- Pushing image ---"
docker push "${QUAY_IMAGE}:${IMAGE_TAG}"

echo ""
echo "Done! Image pushed: ${QUAY_IMAGE}:${IMAGE_TAG}"
echo ""
echo "To deploy to CRC, update the IPP Helm values to use:"
echo "  image: ${QUAY_IMAGE}:${IMAGE_TAG}"
