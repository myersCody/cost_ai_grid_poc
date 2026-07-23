#!/usr/bin/env bash
# Build and push the llm-katan echo LLM image to quay.io.
#
# llm-katan is a lightweight echo LLM used for integration testing —
# it responds to OpenAI-compatible API calls without a real model.
#
# Usage:
#   bash scripts/build-llm-katan-image.sh [VERSION] [IMAGE_TAG]
#
# Defaults:
#   VERSION    — "0.20.2" (last tested working version)
#   IMAGE_TAG  — "latest"
#
# Prerequisites:
#   - Docker running, logged into quay.io (docker login quay.io)
#   - Quay repo exists: quay.io/martin_povolny/llm-katan

set -euo pipefail

QUAY_IMAGE="quay.io/martin_povolny/llm-katan"
VERSION="${1:-0.20.2}"
IMAGE_TAG="${2:-latest}"

echo "--- Building llm-katan:${VERSION} (linux/amd64 + linux/arm64) ---"

docker buildx create --use --name llm-katan-builder 2>/dev/null || docker buildx use llm-katan-builder

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --provenance=false \
  --sbom=false \
  --push \
  -t "${QUAY_IMAGE}:${IMAGE_TAG}" \
  -t "${QUAY_IMAGE}:${VERSION}" \
  --build-arg VERSION="${VERSION}" \
  -f - . <<'EOF'
FROM python:3.11-slim
ARG VERSION=0.20.2
RUN pip install --no-cache-dir llm-katan==${VERSION}
EXPOSE 8000
ENTRYPOINT ["llm-katan"]
CMD ["--model", "test-model", "--backend", "echo", "--providers", "openai,anthropic", "--host", "0.0.0.0"]
EOF

echo ""
echo "Done! Pushed:"
echo "  ${QUAY_IMAGE}:${IMAGE_TAG}"
echo "  ${QUAY_IMAGE}:${VERSION}"
