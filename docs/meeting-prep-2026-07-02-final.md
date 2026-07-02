# CRC Deployment - Complete

**Date:** 2026-07-02  
**Branch:** openshift-deployment  
**Status:** ✅ Deployed and Running

## What We Built

Fully deployed cost-event-consumer + OSAC stack to local CRC (CodeReady Containers) for testing.

## Architecture

```
CRC Cluster
├─ Infrastructure (cluster-wide)
│  ├─ cert-manager (TLS certificate management)
│  ├─ trust-manager (CA distribution)
│  └─ CloudNativePG operator
│
├─ postgres namespace
│  └─ PostgreSQL cluster (2 replicas: osac-1, osac-2)
│     └─ service database (used by OSAC gRPC)
│
├─ osac namespace
│  ├─ osac-oidc: Python OIDC server (HTTPS:8013)
│  ├─ osac-grpc: fulfillment-service gRPC API (:8010)
│  └─ osac-rest: REST gateway (:8000)
│
└─ cost-mgmt namespace
   ├─ cost-db: PostgreSQL (:5432)
   └─ cost-event-consumer (:8020 HTTP, :9000 metrics)
```

## Key Accomplishments

1. **OSAC Deployment** - Successfully deployed OSAC fulfillment-service to CRC
   - Used official OSAC image: `ghcr.io/osac-project/fulfillment-service:main`
   - CloudNativePG handled migrations cleanly (avoided go-migrate dirty bug)
   - Simplified stack (no Keycloak/Authorino) to fit CRC resources

2. **OIDC Server Fix** - Fixed crashing Python OIDC server
   - Installed cryptography dependency
   - Mounted TLS certs from osac-grpc-tls secret
   - Verified endpoints working: `/.well-known/openid-configuration`, `/jwks.json`

3. **Consumer Deployment** - cost-event-consumer running and connecting to OSAC
   - Using quay.io/martin_povolny/cost-event-consumer:latest
   - Connects to osac-rest.osac.svc:8000
   - Gets 401 errors (expected with dummy token)

## Documentation Created

| File | Purpose |
|------|---------|
| `docs/dev/crc-full-deployment.md` | Complete step-by-step deployment guide |
| `docs/dev/crc-osac-deployment.md` | OSAC-specific details and troubleshooting |
| `snippets/test-crc-deployment.sh` | Automated verification script |
| `deploy/k8s/osac-oidc-fixed.yaml` | Working OIDC server manifest |

## Current State

```bash
kubectl get pods --all-namespaces | grep -E "osac|postgres|cost-mgmt"
```

**All 12 pods running:**
- cert-manager: 4 pods (manager, cainjector, webhook, trust-manager)
- postgres: 3 pods (operator + osac-1, osac-2)
- osac: 3 pods (oidc, grpc, rest)
- cost-mgmt: 2 pods (cost-db, consumer)

## Next Steps for Demo

To run the full demo scenario:

1. **Generate OSAC token** (requires cryptography installed locally):
   ```bash
   pip3 install cryptography
   python3 inventory-watcher/scripts/gen_token.py > /tmp/osac_token.txt
   ```

2. **Update consumer with real token**:
   ```bash
   kubectl create secret generic -n cost-mgmt cost-consumer-secrets \
     --from-literal=osac-token="$(cat /tmp/osac_token.txt)" \
     --dry-run=client -o yaml | kubectl apply -f -
   
   kubectl delete pod -n cost-mgmt -l app=cost-event-consumer
   ```

3. **Create test data** in OSAC (via REST API or gRPC)

4. **Verify** consumer ingests events and creates cost data

## Key Technical Decisions

1. **CloudNativePG vs plain postgres** - CNPG handles migrations correctly
2. **Simplified auth** - No Keycloak (used Python OIDC server for token validation)
3. **Official images** - Used upstream OSAC image, not custom builds
4. **Resource optimization** - Removed controller, Authorino to fit CRC limits

## Challenges Overcome

1. **Go-migrate dirty bug** - Manual manifests hit migration issues; CNPG avoided it
2. **Multus CNI namespace isolation** - Required cluster-admin initially, then anyuid SCC
3. **OIDC server crashes** - Missing cryptography dependency, fixed with pip install
4. **Helm chart complexity** - Full INSTALL.md too resource-heavy; created simplified stack

## Files Changed

```
deploy/k8s/osac-manual.yaml           # Initial attempt (kept for reference)
deploy/k8s/osac-oidc-fixed.yaml      # Working OIDC deployment
docs/dev/crc-full-deployment.md       # Complete deployment guide
docs/dev/crc-osac-deployment.md       # OSAC details
snippets/test-crc-deployment.sh       # Verification script
```

## Verification

```bash
# Quick health check
kubectl get pods -n osac -n cost-mgmt -n postgres

# Consumer logs
kubectl logs -n cost-mgmt -l app=cost-event-consumer --tail=20

# OSAC services
kubectl get svc -n osac
```

**Expected:** All pods Running, consumer showing 401 errors (until real token provided).

## For Reproduction

Follow `docs/dev/crc-full-deployment.md` - complete step-by-step guide tested and working.
