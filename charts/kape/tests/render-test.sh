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

# 1. Defaults, no secretRef: monitor env wired, API key absent, monitor
#    disabled (#231 semantics), external ipBlock egress derived from the
#    default base URL, DNS + kube API kept open.
helm template t "$CHART" > "$TMP/default.yaml"
assert_has "$TMP/default.yaml" 'name: kape-controller' "default: controller deployment"
assert_has "$TMP/default.yaml" 'name: KAPE_MONITOR_INTERVAL_SECONDS' "default: interval env"
assert_has "$TMP/default.yaml" 'name: KAPE_MONITOR_SUMMARIZER_MODEL' "default: summarizer env"
assert_has "$TMP/default.yaml" 'name: KAPE_MONITOR_CLASSIFIER_MODEL' "default: classifier env"
assert_has "$TMP/default.yaml" 'name: KAPE_MONITOR_BASE_URL' "default: base URL env"
assert_has "$TMP/default.yaml" 'value: "https://openrouter.ai/api/v1"' "default: spec base URL"
assert_not "$TMP/default.yaml" 'KAPE_MONITOR_OPENROUTER_API_KEY' "default: no API key env"
assert_has "$TMP/default.yaml" 'name: kape-controller-egress' "default: NetworkPolicy"
assert_has "$TMP/default.yaml" 'ipBlock:' "default: external ipBlock shape"
assert_has "$TMP/default.yaml" '10.0.0.0/8' "default: private CIDR exclusion"
assert_has "$TMP/default.yaml" 'port: 443' "default: port 443"

# 2. secretRef set: API key injected via secretKeyRef.
helm template t "$CHART" --set controller.monitor.openrouterApiKey.secretRef=monitor-key \
  --set controller.monitor.openrouterApiKey.key=API_KEY > "$TMP/secret.yaml"
assert_has "$TMP/secret.yaml" 'name: KAPE_MONITOR_OPENROUTER_API_KEY' "secretRef: API key env"
assert_has "$TMP/secret.yaml" 'name: "monitor-key"' "secretRef: secret name"
assert_has "$TMP/secret.yaml" 'key: "API_KEY"' "secretRef: secret key"

# 3. In-cluster base URL: namespace-selector shape, namespace + port derived
#    from the FQDN — no ipBlock monitor rule.
helm template t "$CHART" --set controller.monitor.baseUrl=http://model-gateway-http.aperture.svc.cluster.local/v1 \
  > "$TMP/incluster-full.yaml"
assert_has "$TMP/incluster-full.yaml" 'kubernetes.io/metadata.name: "aperture"' "in-cluster: namespace from FQDN"
assert_has "$TMP/incluster-full.yaml" 'port: 80' "in-cluster: http default port"
helm template t "$CHART" --set controller.monitor.baseUrl=https://gw.kape-system.svc:8443 \
  > "$TMP/incluster-short.yaml"
assert_has "$TMP/incluster-short.yaml" 'kubernetes.io/metadata.name: "kape-system"' "in-cluster: short FQDN"
assert_has "$TMP/incluster-short.yaml" 'port: 8443' "in-cluster: explicit port"

# 4. networkPolicy.enabled=false: no NetworkPolicy rendered.
helm template t "$CHART" --set controller.networkPolicy.enabled=false > "$TMP/nonp.yaml"
assert_not "$TMP/nonp.yaml" 'kape-controller-egress' "disabled NP: not rendered"
assert_has "$TMP/nonp.yaml" 'name: KAPE_MONITOR_BASE_URL' "disabled NP: monitor env still wired"

echo "OK: all chart render assertions passed"
