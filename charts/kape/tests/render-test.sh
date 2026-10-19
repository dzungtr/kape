#!/usr/bin/env bash
# Rendered-manifest checks for the KapeAgentServer cut-over (ADR-0087, #263).
# Runs `helm template` and asserts on the output — no cluster required.
# Usage: charts/kape/tests/render-test.sh
set -euo pipefail
cd "$(dirname "$0")/../.."   # charts/
CHART=kape
fail() { echo "FAIL: $*" >&2; exit 1; }
assert_has() { grep -q "$2" "$1" || fail "$3: expected pattern [$2] not found"; }
assert_not() { grep -q "$2" "$1" && fail "$3: unexpected pattern [$2] found" || true; }
# cr_block extracts the rendered KapeAgentServer CR object (the CRD schema in the
# install-hook ConfigMap also mentions these fields, so block-scope the checks).
cr_block() { awk '/^kind: KapeAgentServer$/{f=1} f&&/^---$/{exit} f' "$1"; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

# 1. Defaults: one declarative path — CRD + default KapeAgentServer CR + operator
#    wiring, and NO hand-wired controller Deployment (clean cut, D12).
helm template t "$CHART" > "$TMP/default.yaml"
assert_has "$TMP/default.yaml" 'kind: KapeAgentServer' "default: KapeAgentServer CR"
assert_has "$TMP/default.yaml" 'name: default' "default: CR name"
assert_has "$TMP/default.yaml" 'ghcr.io/dzungtr/kape-controller:0.1.0' "default: controller image"
assert_has "$TMP/default.yaml" 'ghcr.io/dzungtr/pi-openshell:openrouter' "default: sandbox image"
assert_has "$TMP/default.yaml" 'cpu: "1"' "default: sandbox cpu"
assert_has "$TMP/default.yaml" 'memory: "2Gi"' "default: sandbox memory"
assert_has "$TMP/default.yaml" 'http://model-gateway-http.aperture.svc.cluster.local/v1' "default: model gateway url"
assert_has "$TMP/default.yaml" 'kape default agent server' "default: description"
# No legacy controller Deployment: no Deployment named exactly kape-controller.
assert_not "$TMP/default.yaml" '^  name: kape-controller$' "default: legacy controller Deployment must be gone"
assert_not "$TMP/default.yaml" 'name: kape-controller-egress' "default: legacy controller NetworkPolicy must be gone"
assert_has "$TMP/default.yaml" 'kape.io_kapeagentservers.yaml' "default: KapeAgentServer CRD in the install hook ConfigMap"

# The sandbox-reader ClusterRole stays chart-owned (cluster-scoped grant), its
# binding is per-CR now — the operator renders it.
assert_has "$TMP/default.yaml" 'name: kape-controller-sandbox-reader' "default: sandbox-reader ClusterRole"
assert_not "$TMP/default.yaml" 'name: kape-controller-sandbox-reader-' "default: no chart-side binding"

# Operator-side cluster wiring env (D5): copied into every rendered controller.
for env in KAPE_OPERATOR_CONTROLLER_GATEWAY_ADDR \
           KAPE_OPERATOR_CONTROLLER_CREDS_DIR \
           KAPE_OPERATOR_CONTROLLER_CREDS_SECRET \
           KAPE_OPERATOR_CONTROLLER_DEFAULT_PROVIDER \
           KAPE_OPERATOR_CONTROLLER_TRANSPORT \
           KAPE_OPERATOR_CONTROLLER_EVENT_STORE; do
  assert_has "$TMP/default.yaml" "name: $env" "default: operator wiring env $env"
done
assert_has "$TMP/default.yaml" 'name: KAPE_OPERATOR_CONTROLLER_EXTRA_ENV' "default: passthrough env list"
assert_has "$TMP/default.yaml" 'KAPE_MONITOR_INTERVAL_SECONDS' "default: monitor interval passthrough"
assert_has "$TMP/default.yaml" 'KAPE_REVIVE_MAX' "default: revive passthrough"
assert_not "$TMP/default.yaml" 'KAPE_MONITOR_OPENROUTER_API_KEY' "default: no API key env without secretRef"

# 2. Monitor API key secretRef: rides the passthrough list as a secretKeyRef.
helm template t "$CHART" --set controller.monitor.openrouterApiKey.secretRef=monitor-key \
  --set controller.monitor.openrouterApiKey.key=API_KEY > "$TMP/secret.yaml"
assert_has "$TMP/secret.yaml" 'KAPE_MONITOR_OPENROUTER_API_KEY' "secretRef: API key passthrough"
assert_has "$TMP/secret.yaml" '\\"name\\":\\"monitor-key\\"' "secretRef: secret name"
assert_has "$TMP/secret.yaml" '\\"key\\":\\"API_KEY\\"' "secretRef: secret key"

# 3. CR field overrides flow into the CR (never into a chart-templated pod).
helm template t "$CHART" --set agentServer.sandbox.model=z-ai/glm-5.2 \
  --set agentServer.description="sre triage" > "$TMP/model.yaml"
assert_has "$TMP/model.yaml" 'model: "z-ai/glm-5.2"' "model: rendered into the CR"
assert_has "$TMP/model.yaml" 'description: "sre triage"' "description: rendered into the CR"

# 4. Ingress: rendered into the CR's network group, TLS only when a secret is set.
helm template t "$CHART" --set agentServer.network.ingress.enabled=true \
  --set agentServer.network.ingress.host=sre.kape.example.com \
  --set agentServer.network.ingress.tlsSecretName=sre-tls > "$TMP/ingress.yaml"
cr_block "$TMP/ingress.yaml" > "$TMP/ingress.cr.yaml"
assert_has "$TMP/ingress.cr.yaml" 'host: "sre.kape.example.com"' "ingress: host in CR"
assert_has "$TMP/ingress.cr.yaml" 'tlsSecretName: "sre-tls"' "ingress: TLS secret in CR"
assert_not "$TMP/ingress.yaml" 'kind: Ingress' "ingress: the operator renders the Ingress, not the chart"
helm template t "$CHART" --set agentServer.network.ingress.enabled=true \
  --set agentServer.network.ingress.host=sre.kape.example.com > "$TMP/ingress-plain.yaml"
cr_block "$TMP/ingress-plain.yaml" > "$TMP/ingress-plain.cr.yaml"
assert_not "$TMP/ingress-plain.cr.yaml" 'tlsSecretName' "ingress: no TLS without a secret"

# 5. Ingress enabled without a host must fail loudly.
if helm template t "$CHART" --set agentServer.network.ingress.enabled=true > "$TMP/bad.yaml" 2>"$TMP/bad.err"; then
  fail "ingress without host: expected render failure"
fi
grep -q "agentServer.network.ingress.host is required" "$TMP/bad.err" || fail "ingress without host: unclear error"

# 6. extraEnv / extraEnvFrom passthrough merge with the chart's feature wiring.
helm template t "$CHART" \
  --set controllerWiring.extraEnv[0].name=KAPE_DAEMON_TOKEN \
  --set controllerWiring.extraEnv[0].value=tok \
  --set controllerWiring.extraEnvFrom[0].configMapRef.name=controller-extra > "$TMP/extra.yaml"
assert_has "$TMP/extra.yaml" 'KAPE_DAEMON_TOKEN' "extraEnv: custom entry merged"
assert_has "$TMP/extra.yaml" 'controller-extra' "extraEnvFrom: custom entry merged"

echo "OK: all chart render assertions passed"
