#!/bin/bash
# Source this file to set up demo environment variables.
# Usage: source snippets/env.sh

export FULFILLMENT_SERVICE_DIR=/Users/mpovolny/Projects/fulfillment-service
export OSAC_BASE_URL=http://localhost:8011
export OSAC_TOKEN=$(cat /tmp/osac_token.txt)
export INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb
export INGEST_LISTEN_ADDR=localhost:8020
export RECONCILE_INTERVAL=5m
export SUMMARIZE_INTERVAL=5m

# Derived — useful for demo commands
export TOKEN="$OSAC_TOKEN"
export BASE="$OSAC_BASE_URL"
