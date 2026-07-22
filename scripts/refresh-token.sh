#!/usr/bin/env bash
# Refresh the OSAC JWT in the cost-consumer-secrets and restart the consumer.
#
# Tokens are signed with the osac-oidc-tls private key (not osac-grpc-tls).
# They expire after 7 days; run this script whenever:
#   - CRC was restarted (cert rotation generates a new key)
#   - The consumer shows "token is not valid" in its logs
#
# Requirements: python3, pip packages cryptography + pyjwt
#   pip install cryptography pyjwt
#
# Usage:
#   ./scripts/refresh-token.sh
#   ./scripts/refresh-token.sh --dry-run   # print token, don't apply

set -euo pipefail

DRY_RUN=false
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=true

eval "$(crc oc-env)"

echo "Extracting osac-oidc-tls signing key..."
KEY_FILE=$(mktemp /tmp/osac-oidc-tls.XXXXXX.key)
trap 'rm -f "$KEY_FILE"' EXIT
oc get secret osac-oidc-tls -n osac \
  -o jsonpath='{.data.tls\.key}' | base64 -d > "$KEY_FILE"

echo "Generating JWT..."
TOKEN=$(python3 - <<PYEOF
import json, hashlib, base64, datetime, sys
from cryptography.hazmat.primitives.serialization import load_pem_private_key
try:
    import jwt
except ImportError:
    print("ERROR: pyjwt not installed. Run: pip install pyjwt", file=sys.stderr)
    sys.exit(1)

with open("$KEY_FILE", "rb") as f:
    pk = load_pem_private_key(f.read(), password=None)

pub = pk.public_key().public_numbers()

def b64url(n):
    l = (n.bit_length() + 7) // 8
    return base64.urlsafe_b64encode(n.to_bytes(l, "big")).rstrip(b"=").decode()

jwk = {"e": b64url(pub.e), "kty": "RSA", "n": b64url(pub.n)}
tp = json.dumps(jwk, separators=(",", ":"), sort_keys=True)
kid = base64.urlsafe_b64encode(hashlib.sha256(tp.encode()).digest()).rstrip(b"=").decode()

now = datetime.datetime.now(datetime.timezone.utc)
token = jwt.encode(
    {
        "iss": "https://osac-oidc.osac.svc:8013",
        "sub": "admin",
        "preferred_username": "admin",
        "groups": ["admins"],
        "iat": now,
        "exp": now + datetime.timedelta(days=7),
    },
    pk,
    algorithm="RS256",
    headers={"kid": kid},
)
print(token)
PYEOF
)

if $DRY_RUN; then
  echo ""
  echo "Token (dry-run, not applied):"
  echo "$TOKEN"
  exit 0
fi

echo "Updating cost-consumer-secrets..."
kubectl create secret generic cost-consumer-secrets \
  -n cost-mgmt \
  --from-literal=osac-token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Restarting consumer pod..."
kubectl delete pod -n cost-mgmt -l app=cost-event-consumer

echo "Waiting for consumer to be ready..."
kubectl wait pod -n cost-mgmt -l app=cost-event-consumer \
  --for=condition=Ready --timeout=120s

echo ""
echo "Done. Verifying — last 5 log lines:"
kubectl logs -n cost-mgmt -l app=cost-event-consumer --tail=5
