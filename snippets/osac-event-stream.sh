#!/usr/bin/env bash
# Pseudo-random stream of OSAC CloudEvents — VMs, clusters, MaaS inference.
#
# Creates, heartbeats, and deletes resources over time. Standalone script.
#
# Usage:
#   ./snippets/osac-event-stream.sh                    # default: 60s, moderate rate
#   ./snippets/osac-event-stream.sh --duration 300     # run for 5 minutes
#   ./snippets/osac-event-stream.sh --rate 10          # 10 events/second
#   ./snippets/osac-event-stream.sh --vms 50           # start with 50 VMs
#
# Prerequisites:
#   cost-event-consumer running on localhost:8020

set -uo pipefail

TARGET="${TARGET:-http://localhost:8020}"
DURATION="${DURATION:-60}"
RATE="${RATE:-5}"
NUM_VMS="${NUM_VMS:-10}"
NUM_MODELS="${NUM_MODELS:-3}"

while [[ $# -gt 0 ]]; do
  case $1 in
    --duration) DURATION="$2"; shift 2 ;;
    --rate) RATE="$2"; shift 2 ;;
    --vms) NUM_VMS="$2"; shift 2 ;;
    --models) NUM_MODELS="$2"; shift 2 ;;
    --target) TARGET="$2"; shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

TENANTS=("tenant-acme" "tenant-globex" "tenant-initech" "tenant-umbrella")
VM_STATES=("COMPUTE_INSTANCE_STATE_RUNNING")
CLUSTER_STATES=("CLUSTER_STATE_READY")
MODELS=("llama-3-70b" "claude-sonnet" "granite-34b" "mistral-7b" "gemma-2b")
PROVIDERS=("vllm" "anthropic" "ibm" "mistral" "google")

BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

# Track live resources
declare -a LIVE_VMS=()
declare -a LIVE_CLUSTERS=()
EVENT_COUNT=0
ERRORS=0
INTERVAL=$(echo "scale=3; 1.0 / $RATE" | bc)

rand_int() { echo $(( RANDOM % ($2 - $1 + 1) + $1 )); }
rand_elem() { local arr=("$@"); echo "${arr[RANDOM % ${#arr[@]}]}"; }
rand_id() { printf '%04x%04x' $RANDOM $RANDOM; }

send_event() {
  local json="$1"
  local type="$2"
  local status
  status=$(curl -sf -o /dev/null -w '%{http_code}' -X POST "$TARGET/api/v1/events" \
    -H 'Content-Type: application/json' -d "$json" 2>/dev/null)
  EVENT_COUNT=$((EVENT_COUNT + 1))
  if [[ "$status" -ge 200 && "$status" -lt 300 ]]; then
    return 0
  else
    ERRORS=$((ERRORS + 1))
    return 1
  fi
}

create_vm() {
  local vm_id="vm-$(rand_id)"
  local tenant=$(rand_elem "${TENANTS[@]}")
  local cores=$(rand_elem 2 4 8 16)
  local mem=$(rand_elem 4 8 16 32 64)
  local event_id="evt-$(rand_id)-$(rand_id)"
  local now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  local json=$(cat <<EOJSON
{"specversion":"1.0","type":"osac.compute_instance.lifecycle","source":"osac/event-stream","id":"${event_id}","time":"${now}","data":{"tenant_id":"${tenant}","instance_id":"${vm_id}","template":"osac.templates.vm_medium","state":"COMPUTE_INSTANCE_STATE_RUNNING","cores":${cores},"memory_gib":${mem},"duration_seconds":60,"cpu_core_seconds":$((cores * 60)),"memory_gib_seconds":$((mem * 60))}}
EOJSON
  )

  if send_event "$json" "VM_CREATE"; then
    LIVE_VMS+=("$vm_id")
    echo -e "  ${GREEN}+VM${RESET} ${vm_id} (${cores}c/${mem}G) → ${tenant}"
  fi
}

heartbeat_vm() {
  local vm_id="$1"
  local tenant=$(rand_elem "${TENANTS[@]}")
  local cores=$(rand_elem 2 4 8 16)
  local mem=$(rand_elem 8 16 32)
  local event_id="evt-$(rand_id)-$(rand_id)"
  local now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  local json=$(cat <<EOJSON
{"specversion":"1.0","type":"osac.compute_instance.lifecycle","source":"osac/event-stream","id":"${event_id}","time":"${now}","data":{"tenant_id":"${tenant}","instance_id":"${vm_id}","state":"COMPUTE_INSTANCE_STATE_RUNNING","cores":${cores},"memory_gib":${mem},"duration_seconds":60,"cpu_core_seconds":$((cores * 60)),"memory_gib_seconds":$((mem * 60))}}
EOJSON
  )

  if send_event "$json" "VM_HEARTBEAT"; then
    echo -e "  ${DIM}♥VM${RESET} ${vm_id}"
  fi
}

delete_vm() {
  local idx=$((RANDOM % ${#LIVE_VMS[@]}))
  local vm_id="${LIVE_VMS[$idx]}"
  local event_id="evt-$(rand_id)-$(rand_id)"
  local now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  local json=$(cat <<EOJSON
{"specversion":"1.0","type":"osac.compute_instance.lifecycle","source":"osac/event-stream","id":"${event_id}","time":"${now}","data":{"tenant_id":"tenant-acme","instance_id":"${vm_id}","state":"COMPUTE_INSTANCE_STATE_STOPPED","cores":4,"memory_gib":16,"duration_seconds":30,"cpu_core_seconds":120,"memory_gib_seconds":480}}
EOJSON
  )

  if send_event "$json" "VM_DELETE"; then
    unset 'LIVE_VMS[$idx]'
    LIVE_VMS=("${LIVE_VMS[@]}")
    echo -e "  ${RED}-VM${RESET} ${vm_id}"
  fi
}

send_maas_event() {
  local model_idx=$((RANDOM % NUM_MODELS))
  local model="${MODELS[$model_idx]}"
  local provider="${PROVIDERS[$model_idx]}"
  local tenant=$(rand_elem "${TENANTS[@]}")
  local tokens_in=$(rand_int 100 5000)
  local tokens_out=$(rand_int 50 2000)
  local cached=$((RANDOM % 500))
  local reasoning=$((RANDOM % 1000))
  local event_id="evt-$(rand_id)-$(rand_id)"
  local now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  local json=$(cat <<EOJSON
{"specversion":"1.0","type":"inference.tokens.used","source":"maas-gateway","id":"${event_id}","time":"${now}","subject":"${tenant}","data":{"user":"user-${tenant}","group":"${tenant}","subscription":"${tenant}/default-sub","provider":"${provider}","model":"${model}","prompt_tokens":${tokens_in},"completion_tokens":${tokens_out},"total_tokens":$((tokens_in + tokens_out)),"cached_input_tokens":${cached},"reasoning_tokens":${reasoning},"duration_ms":$((RANDOM % 3000 + 200)),"tenant_id":"${tenant}","model_id":"model-${model}"}}
EOJSON
  )

  if send_event "$json" "MAAS"; then
    echo -e "  ${BLUE}⚡MaaS${RESET} ${model} ${tokens_in}→${tokens_out} tok (${tenant})"
  fi
}

send_cluster_heartbeat() {
  local cluster_id="cluster-$(rand_id)"
  local tenant=$(rand_elem "${TENANTS[@]}")
  local nodes=$(rand_int 3 10)
  local event_id="evt-$(rand_id)-$(rand_id)"
  local now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  local json=$(cat <<EOJSON
{"specversion":"1.0","type":"osac.cluster.lifecycle","source":"osac/event-stream","id":"${event_id}","time":"${now}","data":{"tenant_id":"${tenant}","cluster_id":"${cluster_id}","template":"osac.templates.hcp_medium","state":"CLUSTER_STATE_READY","host_type":"_control_plane","duration_seconds":60,"worker_node_seconds":$((nodes * 60)),"node_count":${nodes}}}
EOJSON
  )

  if send_event "$json" "CLUSTER"; then
    echo -e "  ${YELLOW}◆Cluster${RESET} ${cluster_id} (${nodes} nodes) → ${tenant}"
  fi
}

# ── Main loop ──

echo -e "${BOLD}OSAC Event Stream${RESET}"
echo -e "${DIM}  target:   ${TARGET}${RESET}"
echo -e "${DIM}  duration: ${DURATION}s${RESET}"
echo -e "${DIM}  rate:     ~${RATE} events/s${RESET}"
echo -e "${DIM}  vms:      ${NUM_VMS} initial${RESET}"
echo -e "${DIM}  models:   ${NUM_MODELS}${RESET}"
echo ""

# Create initial VMs
echo -e "${BOLD}Creating initial VMs...${RESET}"
for ((i = 0; i < NUM_VMS; i++)); do
  create_vm
done
echo ""

END_TIME=$((SECONDS + DURATION))
echo -e "${BOLD}Streaming events...${RESET}"

while [[ $SECONDS -lt $END_TIME ]]; do
  # Weighted random action
  action=$((RANDOM % 100))

  if [[ $action -lt 70 ]]; then
    # 70% MaaS inference (dominant workload — realistic for AI platform)
    send_maas_event
  elif [[ $action -lt 80 ]]; then
    # 10% VM heartbeat
    if [[ ${#LIVE_VMS[@]} -gt 0 ]]; then
      vm_idx=$((RANDOM % ${#LIVE_VMS[@]}))
      heartbeat_vm "${LIVE_VMS[$vm_idx]}"
    fi
  elif [[ $action -lt 88 ]]; then
    # 8% cluster heartbeat
    send_cluster_heartbeat
  elif [[ $action -lt 95 ]]; then
    # 7% create VM
    create_vm
  else
    # 5% delete VM (if we have enough)
    if [[ ${#LIVE_VMS[@]} -gt 3 ]]; then
      delete_vm
    else
      create_vm
    fi
  fi

  sleep "$INTERVAL" 2>/dev/null || sleep 1
done

echo ""
echo -e "${BOLD}Stream complete${RESET}"
echo -e "  Events sent: ${EVENT_COUNT}"
echo -e "  Errors:      ${ERRORS}"
echo -e "  Live VMs:    ${#LIVE_VMS[@]}"
echo -e "  Duration:    ${DURATION}s"
