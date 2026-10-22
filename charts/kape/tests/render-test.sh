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

# Model gateway URL: no chart default and no compiled-in default — the
# KapeAgentServer CR owns it, every render supplies one, and case 0 proves an
# omitted value fails loudly.
GATEWAY_URL=http://model-gateway.kape-system.svc.cluster.local/v1
helm_t() { helm template t "$CHART" --set agentServer.modelGateway.url="$GATEWAY_URL" "$@"; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

# 0. Model gateway URL is required: the render fails without it.
if helm template t "$CHART" > "$TMP/nogw.yaml" 2>"$TMP/nogw.err"; then
  fail "model gateway: expected render failure when agentServer.modelGateway.url is unset"
fi
grep -q "agentServer.modelGateway.url is required" "$TMP/nogw.err" || fail "model gateway: unclear error"

# 1. Defaults: one declarative path — CRD + default KapeAgentServer CR + operator
#    wiring, and NO hand-wired controller Deployment (clean cut, D12).
helm_t > "$TMP/default.yaml"
assert_has "$TMP/default.yaml" 'kind: KapeAgentServer' "default: KapeAgentServer CR"
assert_has "$TMP/default.yaml" 'name: default' "default: CR name"
assert_has "$TMP/default.yaml" 'ghcr.io/dzungtr/kape-controller:0.1.0' "default: controller image"
assert_has "$TMP/default.yaml" 'ghcr.io/dzungtr/pi-openshell:openrouter' "default: sandbox image"
assert_has "$TMP/default.yaml" 'cpu: "1"' "default: sandbox cpu"
assert_has "$TMP/default.yaml" 'memory: "2Gi"' "default: sandbox memory"
assert_has "$TMP/default.yaml" "$GATEWAY_URL" "default: model gateway url (from the caller, not a default)"
assert_has "$TMP/default.yaml" 'kape default agent server' "default: description"
# No legacy controller Deployment: no Deployment named exactly kape-controller.
assert_not "$TMP/default.yaml" '^  name: kape-controller$' "default: legacy controller Deployment must be gone"
assert_not "$TMP/default.yaml" 'name: kape-controller-egress' "default: legacy controller NetworkPolicy must be gone"
assert_has "$TMP/default.yaml" 'kape.io_kapeagentservers.yaml' "default: KapeAgentServer CRD in the install hook ConfigMap"

# The default CR must be admissible as rendered: an explicit empty credentials
# list (the CRD's unique-name CEL rule tolerates an omitted list, and the
# explicit [] keeps the rendered contract readable) and no empty model key.
cr_block "$TMP/default.yaml" > "$TMP/default.cr.yaml"
assert_has "$TMP/default.cr.yaml" '^  credentials:$' "default: credentials rendered explicitly"
awk '/^  credentials:$/{getline; print; exit}' "$TMP/default.cr.yaml" | grep -q '^    \[\]$' \
  || fail "default: credentials must render an explicit empty list"
assert_not "$TMP/default.cr.yaml" '^    model:' "default: no model key when the value is empty"

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
helm_t --set controller.monitor.openrouterApiKey.secretRef=monitor-key \
  --set controller.monitor.openrouterApiKey.key=API_KEY > "$TMP/secret.yaml"
assert_has "$TMP/secret.yaml" 'KAPE_MONITOR_OPENROUTER_API_KEY' "secretRef: API key passthrough"
assert_has "$TMP/secret.yaml" '\\"name\\":\\"monitor-key\\"' "secretRef: secret name"
assert_has "$TMP/secret.yaml" '\\"key\\":\\"API_KEY\\"' "secretRef: secret key"

# 3. CR field overrides flow into the CR (never into a chart-templated pod).
helm_t --set agentServer.sandbox.model=z-ai/glm-5.2 \
  --set agentServer.description="sre triage" > "$TMP/model.yaml"
assert_has "$TMP/model.yaml" 'model: "z-ai/glm-5.2"' "model: rendered into the CR"
assert_has "$TMP/model.yaml" 'description: "sre triage"' "description: rendered into the CR"

# 4. Ingress: rendered into the CR's network group, TLS only when a secret is set.
helm_t --set agentServer.network.ingress.enabled=true \
  --set agentServer.network.ingress.host=sre.kape.example.com \
  --set agentServer.network.ingress.tlsSecretName=sre-tls > "$TMP/ingress.yaml"
cr_block "$TMP/ingress.yaml" > "$TMP/ingress.cr.yaml"
assert_has "$TMP/ingress.cr.yaml" 'host: "sre.kape.example.com"' "ingress: host in CR"
assert_has "$TMP/ingress.cr.yaml" 'tlsSecretName: "sre-tls"' "ingress: TLS secret in CR"
assert_not "$TMP/ingress.yaml" 'kind: Ingress' "ingress: the operator renders the Ingress, not the chart"
helm_t --set agentServer.network.ingress.enabled=true \
  --set agentServer.network.ingress.host=sre.kape.example.com > "$TMP/ingress-plain.yaml"
cr_block "$TMP/ingress-plain.yaml" > "$TMP/ingress-plain.cr.yaml"
assert_not "$TMP/ingress-plain.cr.yaml" 'tlsSecretName' "ingress: no TLS without a secret"

# 5. Ingress enabled without a host must fail loudly.
if helm_t --set agentServer.network.ingress.enabled=true > "$TMP/bad.yaml" 2>"$TMP/bad.err"; then
  fail "ingress without host: expected render failure"
fi
grep -q "agentServer.network.ingress.host is required" "$TMP/bad.err" || fail "ingress without host: unclear error"

# 6. extraEnv / extraEnvFrom passthrough merge with the chart's feature wiring.
helm_t \
  --set controllerWiring.extraEnv[0].name=KAPE_DAEMON_TOKEN \
  --set controllerWiring.extraEnv[0].value=tok \
  --set controllerWiring.extraEnvFrom[0].configMapRef.name=controller-extra > "$TMP/extra.yaml"
assert_has "$TMP/extra.yaml" 'KAPE_DAEMON_TOKEN' "extraEnv: custom entry merged"
assert_has "$TMP/extra.yaml" 'controller-extra' "extraEnvFrom: custom entry merged"

# 7. OpenShell credential writer (ADR-0088, #265): off by default, enabled by a
#    creds Secret, and read-only on Secrets either way.
assert_not "$TMP/default.yaml" 'KAPE_OPERATOR_OPENSHELL_GATEWAY_ADDR' "default: credential writer disabled without a creds secret"
assert_has "$TMP/default.yaml" 'verbs: \[get, list, watch\]' "rbac: credential Secrets are read-only (get/list/watch)"
helm_t --set operator.openshell.credsSecret=openshell-client-tls > "$TMP/writer.yaml"
assert_has "$TMP/writer.yaml" 'name: KAPE_OPERATOR_OPENSHELL_GATEWAY_ADDR' "writer: gateway env rendered"
assert_has "$TMP/writer.yaml" 'openshell.openshell.svc.cluster.local:8080' "writer: falls back to controllerWiring.gatewayAddr"
assert_has "$TMP/writer.yaml" 'name: KAPE_OPERATOR_OPENSHELL_WORKSPACE' "writer: workspace env rendered"
assert_has "$TMP/writer.yaml" 'secretName: openshell-client-tls' "writer: mTLS secret mounted"
assert_has "$TMP/writer.yaml" 'mountPath: /var/run/kape-operator/openshell-mtls' "writer: mTLS mount path"

echo "OK: all chart render assertions passed"
