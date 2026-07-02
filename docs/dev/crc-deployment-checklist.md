# CRC Deployment Checklist

Quick reference for deploying the full stack. See `crc-full-deployment.md` for detailed steps.

## Prerequisites

- [ ] CRC 4.18+ installed and running
- [ ] `oc` or `kubectl` CLI installed
- [ ] `helm` 3.8+ installed
- [ ] `jq` installed
- [ ] `openssl` installed
- [ ] Admin access to CRC cluster

**Verify CRC:**
```bash
crc status
eval $(crc oc-env)
oc login -u kubeadmin https://api.crc.testing:6443
```

## Deployment Order

### 1. Infrastructure (15 min)

- [ ] Install cert-manager (Helm)
- [ ] Install trust-manager (Helm)
- [ ] Create self-signed CA
- [ ] Create ClusterIssuer (osac-ca)
- [ ] Create CA Bundle for namespace distribution

**Commands:** See `crc-full-deployment.md` Steps 1-3

**Verification:**
```bash
kubectl get pods -n cert-manager
# Expected: 4 pods Running (cert-manager, cainjector, webhook, trust-manager)
```

### 2. CloudNativePG Operator (5 min)

- [ ] Create postgres namespace
- [ ] Grant nonroot-v2 SCC
- [ ] Install CNPG operator (Helm)

**Commands:** See `crc-full-deployment.md` Step 4

**Verification:**
```bash
kubectl get pods -n postgres
# Expected: 1 pod Running (cnpg-cloudnative-pg)
```

### 3. OSAC PostgreSQL Cluster (10 min)

- [ ] Create OSAC database credentials (keycloak, service)
- [ ] Create PostgreSQL TLS certificate
- [ ] Deploy CNPG Cluster (2 instances)
- [ ] Wait for pods ready

**Commands:** See `crc-full-deployment.md` Step 5

**Verification:**
```bash
kubectl get pods -n postgres
# Expected: 3 pods Running (operator, osac-1, osac-2)
kubectl get cluster -n postgres osac
# Expected: Instances=2, Ready=true
```

### 4. OSAC Stack (10 min)

- [ ] Get PostgreSQL service credentials
- [ ] Create osac namespace
- [ ] Deploy OSAC OIDC server (ConfigMap + Deployment)
- [ ] Create gRPC TLS certificate
- [ ] Deploy OSAC gRPC server
- [ ] Deploy OSAC REST gateway

**Commands:** See `crc-full-deployment.md` Step 6

**IMPORTANT:** Replace `${POSTGRES_SERVICE}` and `${POSTGRES_PASSWORD}` with actual values:
```bash
POSTGRES_SERVICE=$(kubectl get secret -n postgres osac-service-credentials -o json | jq -r '.data["username"] | @base64d')
POSTGRES_PASSWORD=$(kubectl get secret -n postgres osac-service-credentials -o json | jq -r '.data["password"] | @base64d')
echo "Service: $POSTGRES_SERVICE, Password: $POSTGRES_PASSWORD"
```

**Verification:**
```bash
kubectl get pods -n osac
# Expected: 3 pods Running (osac-oidc, osac-grpc, osac-rest)

# Test OIDC endpoint
kubectl run curl-test --image=curlimages/curl:latest --rm -i --restart=Never -- \
  curl -k https://osac-oidc.osac.svc:8013/.well-known/openid-configuration
# Expected: JSON with "issuer" and "jwks_uri"
```

### 5. Cost Management Stack (5 min)

- [ ] Create cost-mgmt namespace
- [ ] Deploy cost-db (PostgreSQL)
- [ ] Create cost-db credentials secret
- [ ] Create consumer OSAC token secret (dummy for now)
- [ ] Deploy cost-event-consumer

**Commands:** See `crc-full-deployment.md` Step 7

**Verification:**
```bash
kubectl get pods -n cost-mgmt
# Expected: 2 pods Running (cost-db-0, cost-event-consumer)

kubectl logs -n cost-mgmt -l app=cost-event-consumer --tail=10
# Expected: Logs showing connection to OSAC (401 errors OK with dummy token)
```

### 6. Final Verification (2 min)

- [ ] Run automated test script

```bash
cd ~/Projects/koku/cost_ai_grid_poc
./snippets/test-crc-deployment.sh
```

**OR manually verify:**
```bash
kubectl get pods --all-namespaces | grep -E "osac|postgres|cert-manager|cost-mgmt"
# Expected: 12 pods all Running
```

## Post-Deployment: Generate Real Token

For actual demo/testing:

```bash
# 1. Install cryptography locally
pip3 install cryptography

# 2. Generate token
cd ~/Projects/koku/cost_ai_grid_poc
python3 inventory-watcher/scripts/gen_token.py > /tmp/osac_token.txt

# 3. Update consumer secret
kubectl create secret generic -n cost-mgmt cost-consumer-secrets \
  --from-literal=osac-token="$(cat /tmp/osac_token.txt)" \
  --dry-run=client -o yaml | kubectl apply -f -

# 4. Restart consumer
kubectl delete pod -n cost-mgmt -l app=cost-event-consumer

# 5. Verify no more 401 errors
kubectl logs -n cost-mgmt -l app=cost-event-consumer --tail=20
```

## Common Issues

### OSAC gRPC migrations fail (dirty database)
- ✅ **Solved by using CloudNativePG** - don't use plain postgres:16 image
- CNPG manages migrations correctly

### OSAC OIDC pod crashing
- Check: `kubectl logs -n osac -l app=osac-oidc`
- Fix: Verify cryptography is installed in container startup
- Manifest: `deploy/k8s/osac-oidc-fixed.yaml` has the fix

### Consumer gets 401 errors
- **Expected** with dummy token
- Generate real token (see Post-Deployment above)

### CRC resource exhausted
- This deployment uses ~12 pods
- If needed: `crc config set cpus 6` and `crc config set memory 16384`
- Restart CRC: `crc stop && crc start`

## Cleanup

To start over or remove everything:

```bash
# Remove applications
kubectl delete namespace osac cost-mgmt

# Remove PostgreSQL cluster
kubectl delete cluster -n postgres osac
kubectl delete namespace postgres

# Remove operators (optional - can keep for future deployments)
helm uninstall cnpg -n postgres
helm uninstall trust-manager -n cert-manager
helm uninstall cert-manager -n cert-manager
```

## Timeline

Total deployment time: **~45 minutes**
- Infrastructure setup: 15 min
- PostgreSQL: 15 min
- OSAC stack: 10 min
- Cost Management: 5 min

Faster on subsequent runs (~20 min) since operators are already installed.

## Files Reference

| File | Purpose |
|------|---------|
| `docs/dev/crc-full-deployment.md` | Complete step-by-step guide |
| `docs/dev/crc-deployment-checklist.md` | This checklist (quick reference) |
| `docs/dev/crc-osac-deployment.md` | OSAC-specific details |
| `snippets/test-crc-deployment.sh` | Automated verification |
| `deploy/k8s/osac-oidc-fixed.yaml` | Working OIDC manifest |

## Support

If stuck, check:
1. Pod logs: `kubectl logs -n <namespace> <pod-name>`
2. Pod events: `kubectl describe pod -n <namespace> <pod-name>`
3. This repo's issues/docs for known problems
