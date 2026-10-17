#!/usr/bin/env bash
# Rendered-manifest checks for the SLM monitor deploy wiring (issue #234).
# Runs `helm template` and asserts on the output — no cluster required.
# Usage: charts/kape/tests/render-test.sh
set -euo pipefail
cd "$(dirname "$0")/../.."   # charts/
CHART=kape
fail() { echo "FAIL: $*" >&2; exit 1; }
assert_has() { grep -q "$2" "$1" || fail "$3: expected pattern [$2] not found"; }
assert_not() { grep -q "$2" "$1" && fail "$3: unexpected pattern [$2] found" || true; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

# 1. Defaults, no secretRef: full functional env wired, monitor env present,
#    API key absent (monitor disabled, #231 semantics), NetworkPolicy OFF
#    (default-deny egress would break core traffic — fix round 1).
helm template t "$CHART" > "$TMP/default.yaml"
assert_has "$TMP/default.yaml" 'name: kape-controller' "default: controller deployment"
assert_has "$TMP/default.yaml" 'kind: ServiceAccount' "default: controller ServiceAccount"
assert_has "$TMP/default.yaml" 'kind: ClusterRole' "default: sandbox-reader ClusterRole"
assert_has "$TMP/default.yaml" 'kind: ClusterRoleBinding' "default: sandbox-reader ClusterRoleBinding"
for obj in ClusterRole ClusterRoleBinding; do
  block=$(awk "/kind: $obj/{f=1} f" "$TMP/default.yaml")
  [[ "$block" == *'kape-controller-sandbox-reader'* ]] || fail "default: $obj missing sandbox-reader"
done
binding=$(awk '/kind: ClusterRoleBinding/{f=1} f' "$TMP/default.yaml")
[[ "$binding" == *'name: kape-controller'* ]] || fail "default: binding does not reference kape-controller SA"
# controller ClusterRole rules: sandboxes + get/watch (before the next Source marker)
ctlrole=$(sed -n '/Source: kape\/templates\/controller\/rbac.yaml/,/^---/p' "$TMP/default.yaml")
[[ "$ctlrole" == *'sandboxes'* ]] || fail "default: controller ClusterRole lacks sandboxes resource"
[[ "$ctlrole" == *'watch'* ]] || fail "default: controller ClusterRole lacks watch verb"
[[ "$ctlrole" == *'agents.x-k8s.io'* ]] || fail "default: ClusterRole lacks agents.x-k8s.io group"
assert_has "$TMP/default.yaml" 'name: KAPE_GATEWAY_ADDR' "default: gateway env"
assert_has "$TMP/default.yaml" 'name: KAPE_CREDS_DIR' "default: creds env"
assert_has "$TMP/default.yaml" 'name: KAPE_DEFAULT_IMAGE' "default: image env"
assert_has "$TMP/default.yaml" 'name: KAPE_MODEL_GATEWAY_URL' "default: model-gateway env"
assert_has "$TMP/default.yaml" 'name: KAPE_TRANSPORT' "default: transport env"
assert_has "$TMP/default.yaml" 'name: KAPE_EVENT_STORE' "default: event-store env"
assert_not "$TMP/default.yaml" 'KAPE_MONITOR_OPENROUTER_API_KEY' "default: no API key env"
assert_has "$TMP/default.yaml" 'name: KAPE_MONITOR_INTERVAL_SECONDS' "default: interval env"
assert_has "$TMP/default.yaml" 'name: KAPE_MONITOR_TIMEOUT_SECONDS' "default: timeout env"
assert_has "$TMP/default.yaml" 'value: "https://openrouter.ai/api/v1"' "default: spec base URL"
assert_not "$TMP/default.yaml" 'kape-controller-egress' "default: NetworkPolicy off"

# 2. secretRef set: API key injected via secretKeyRef; NP enabled renders
#    the derived external-host ipBlock rule.
helm template t "$CHART" --set controller.monitor.openrouterApiKey.secretRef=monitor-key \
  --set controller.monitor.openrouterApiKey.key=API_KEY \
  --set controller.networkPolicy.enabled=true > "$TMP/secret.yaml"
assert_has "$TMP/secret.yaml" 'name: KAPE_MONITOR_OPENROUTER_API_KEY' "secretRef: API key env"
assert_has "$TMP/secret.yaml" 'name: "monitor-key"' "secretRef: secret name"
assert_has "$TMP/secret.yaml" 'key: "API_KEY"' "secretRef: secret key"
assert_has "$TMP/secret.yaml" 'name: kape-controller-egress' "NP on: policy rendered"
assert_has "$TMP/secret.yaml" 'ipBlock:' "NP on: external ipBlock shape"
assert_has "$TMP/secret.yaml" '10.0.0.0/8' "NP on: private CIDR exclusion"
assert_has "$TMP/secret.yaml" 'port: 443' "NP on: port 443"

# 3. In-cluster base URL with NP enabled: namespace-selector shape,
#    namespace + port derived from the FQDN — no ipBlock monitor rule.
helm template t "$CHART" --set controller.networkPolicy.enabled=true \
  --set controller.monitor.baseUrl=http://model-gateway-http.aperture.svc.cluster.local/v1 \
  > "$TMP/incluster-full.yaml"
assert_has "$TMP/incluster-full.yaml" 'kubernetes.io/metadata.name: "aperture"' "in-cluster: namespace from FQDN"
assert_has "$TMP/incluster-full.yaml" 'port: 80' "in-cluster: http default port"
helm template t "$CHART" --set controller.networkPolicy.enabled=true \
  --set controller.monitor.baseUrl=https://gw.kape-system.svc:8443 \
  > "$TMP/incluster-short.yaml"
assert_has "$TMP/incluster-short.yaml" 'kubernetes.io/metadata.name: "kape-system"' "in-cluster: short FQDN"
assert_has "$TMP/incluster-short.yaml" 'port: 8443' "in-cluster: explicit port"

# 4. 2-part in-cluster host (gw.svc): render must fail with a clear error
#    (namespace would mis-derive to the service name).
if helm template t "$CHART" --set controller.networkPolicy.enabled=true \
     --set controller.monitor.baseUrl=https://gw.svc/v1 > "$TMP/bad.yaml" 2>"$TMP/bad.err"; then
  fail "2-part FQDN: expected render failure"
fi
grep -q "at least 3 segments" "$TMP/bad.err" || fail "2-part FQDN: unclear error: $(cat "$TMP/bad.err")"

# 5. creds secretRef: mTLS Secret volume-mounted read-only at credsDir.
helm template t "$CHART" --set controller.creds.secretRef=controller-gateway-creds > "$TMP/creds.yaml"
assert_has "$TMP/creds.yaml" 'secretName: "controller-gateway-creds"' "creds: secret volume"
assert_has "$TMP/creds.yaml" 'mountPath: "/var/run/kape-controller/creds"' "creds: mounted at credsDir"

echo "OK: all chart render assertions passed"