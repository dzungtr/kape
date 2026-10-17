#!/usr/bin/env bash
# daemon-as-canonical-main-process live smoke (issue #247, spec #245 E2):
# the controller threads command=[kape-daemon] in CreateSandboxRequest and
# the OpenShell k8s driver launches the daemon as the sandbox's main process
# (no shell, no profile hook). Verified against the live microk8s gateway
# (v0.0.116) with the in-cluster smoke controller.
#
# Legs:
#   1. create: SandboxSpec.command threaded; supervisor env carries
#      OPENSHELL_MAIN_PROCESS_SPEC with kape-daemon; kape-daemon runs as the
#      supervisor's main process (PPID = openshell-sandbox); /status
#      reachable via the #214 podIP pull path (DNAT shim, #217); boot_id
#      captured.
#   2. daemon death: SIGKILL the daemon → workload container terminates;
#      record the sandbox/pod exit semantics (phase, exit code).
#   3. pod restart: delete the sandbox pod → recreated pod re-runs the
#      canonical command; daemon back with a NEW boot_id.
#
# Usage: ./smoke-247.sh
set -uo pipefail

cd "$(dirname "$0")"

NS="${NS:-openshell}"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-ghcr.io/dzungtr/kape-controller:smoke-247}"
DAEMON_IMAGE="${DAEMON_IMAGE:-ghcr.io/dzungtr/kape-daemon:smoke-217d}"
KILLER_IMAGE="${KILLER_IMAGE:-alpine:3.20}"
PF_PORT="${PF_PORT:-3000}"
BASE="http://localhost:${PF_PORT}"
DAEMON_PORT="${DAEMON_PORT:-9090}"
TOKEN="smoke-247-$(head -c 12 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9')"
CTL=kape-smoke247-ctl
NOTE=/tmp/kape-smoke-247
TIMEOUT_READY=300

export CGO_ENABLED=0 GOPATH="${GOPATH:-/tmp/gopath}" GOCACHE="${GOCACHE:-/tmp/gocache}" GOWORK=off

PASS=0; FAIL=0; CREATED_IDS=(); PF_PID=""
mkdir -p "$NOTE"
note()  { echo "== $*"; }
pass()  { PASS=$((PASS+1)); echo "PASS: $*"; }
fail()  { FAIL=$((FAIL+1)); echo "FAIL: $*"; }
assert(){ if eval "$2"; then pass "$1"; else fail "$1"; fi; }

http_code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
jqpy() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1"; }

cleanup() {
    local rc=$?
    note "teardown"
    for id in "${CREATED_IDS[@]:-}"; do
        [ -z "$id" ] && continue
        curl -s -X DELETE "$BASE/agents/$id" >/dev/null 2>&1 || true
    done
    sleep 3
    kubectl delete sandbox -A -l managed-by=kape-controller --ignore-not-found >/dev/null 2>&1 || true
    kubectl delete pod -n "$NS" "$CTL" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1 || true
    kubectl delete pod -n default kape-smoke-killer --ignore-not-found --force --grace-period=0 >/dev/null 2>&1 || true
    sleep 2
    local remaining
    remaining=$(kubectl get sandbox -A --no-headers 2>/dev/null | wc -l)
    if [ "$remaining" = 0 ]; then
        echo "PASS: teardown leaves zero Sandbox CRs"
    else
        echo "FAIL: teardown leaves $remaining Sandbox CRs"; kubectl get sandbox -A; rc=1
    fi
    [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null
    echo "=== smoke-247: $PASS passed, $FAIL failed ==="
    exit $rc
}
trap cleanup EXIT

# --- preflight -------------------------------------------------------------
note "preflight"
kubectl get sandbox -A --no-headers 2>/dev/null | wc -l | grep -qx 0 \
    || { echo "FAIL: preflight expects zero Sandbox CRs"; kubectl get sandbox -A; exit 1; }
pass "preflight: zero Sandbox CRs"

# --- infra -----------------------------------------------------------------
note "start privileged killer pod (hostPID nsenter, #217 pattern)"
kubectl delete pod -n default kape-smoke-killer --ignore-not-found --force --grace-period=0 >/dev/null 2>&1
kubectl run kape-smoke-killer -n default --image="$KILLER_IMAGE" --restart=Never \
    --overrides='{"spec":{"hostPID":true,"containers":[{"name":"c","image":"'"$KILLER_IMAGE"'","command":["sleep","14400"],"volumeMounts":[{"name":"h","mountPath":"/host"}],"securityContext":{"privileged":true}}],"volumes":[{"name":"h","hostPath":{"path":"/"}}]}}' >/dev/null 2>&1 \
    && kubectl wait --for=condition=ready pod/kape-smoke-killer -n default --timeout=120s >/dev/null \
    && pass "killer pod ready" || fail "killer pod not ready"

note "deploy in-cluster smoke controller (KAPE_DAEMON_COMMAND=kape-daemon)"
kubectl delete pod -n "$NS" "$CTL" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1
kubectl run "$CTL" -n "$NS" \
    --image="$CONTROLLER_IMAGE" --restart=Never --port=8081 \
    --overrides="$(cat <<EOF
{"spec":{"serviceAccountName":"kape-controller","containers":[{"name":"ctl","image":"$CONTROLLER_IMAGE","args":["-listen",":8081"],
  "env":[
    {"name":"KAPE_GATEWAY_ADDR","value":"openshell.openshell.svc.cluster.local:8080"},
    {"name":"KAPE_CREDS_DIR","value":"/mtls"},
    {"name":"KAPE_DEFAULT_IMAGE","value":"$DAEMON_IMAGE"},
    {"name":"KAPE_DEFAULT_PROVIDER","value":"openrouter-spike"},
    {"name":"KAPE_TRANSPORT","value":"pull"},
    {"name":"KAPE_SANDBOX_NAMESPACE","value":"$NS"},
    {"name":"KAPE_DAEMON_PORT","value":"$DAEMON_PORT"},
    {"name":"KAPE_DAEMON_TOKEN","value":"$TOKEN"},
    {"name":"KAPE_DAEMON_COMMAND","value":"kape-daemon"},
    {"name":"KAPE_CONTROLLER_SERVICE_PORT","value":"80"}],
  "ports":[{"containerPort":8081,"name":"http"}],
  "volumeMounts":[{"name":"mtls","mountPath":"/mtls","readOnly":true}],
  "imagePullPolicy":"Always"}],
 "serviceAccountName":"kape-controller",
 "imagePullSecrets":[{"name":"ghcr-kape"}],
 "volumes":[{"name":"mtls","secret":{"secretName":"openshell-client-tls"}}]}}
EOF
)" >/dev/null || { echo "FAIL: controller deploy"; exit 1; }
kubectl wait --for=condition=ready pod/"$CTL" -n "$NS" --timeout=180s >/dev/null \
    && kubectl logs -n "$NS" "$CTL" --tail=5 | grep -q "listening" \
    && pass "controller up" || { fail "controller not up"; kubectl logs -n "$NS" "$CTL" --tail=20; }

kubectl port-forward -n "$NS" pod/"$CTL" "${PF_PORT}:8081" >/dev/null 2>&1 &
PF_PID=$!
for i in $(seq 1 20); do [ "$(http_code "$BASE/agents")" = "200" ] && break; sleep 0.5; done
[ "$(http_code "$BASE/agents")" = "200" ] && pass "port-forward up on $BASE" || { fail "port-forward"; exit 1; }

# helpers (from the #217 smoke-failures pattern)
wait_status() { # id, want, timeout → 0 when reached
    local id=$1 want=$2 t=$3 i got
    for i in $(seq 1 $((t*2))); do
        got=$(curl -s "$BASE/agents/$id" | jqpy 'd["status"]' 2>/dev/null) || got=""
        [ "$got" = "$want" ] && return 0
        sleep 0.5
    done
    return 1
}
sandbox_pod() { # agent id → sandbox pod name
    local id=$1 sbx
    sbx="sbx-${id#agent-}"
    kubectl get pods -n "$NS" -l agents.x-k8s.io/sandbox-name="$sbx" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
}
daemon_pid() { # → host PID of kape-daemon (main process)
    kubectl exec -n default kape-smoke-killer -- sh -c '
        for d in /proc/[0-9]*; do
            c=$(tr "\0" " " </proc/$d/cmdline 2>/dev/null)
            case "$c" in *kape-daemon*) echo ${d#/proc/}; exit 0;; esac
        done; exit 1' 2>/dev/null
}
daemon_ppid() { # → parent pid + parent cmdline of the daemon main process
    local p; p=$(daemon_pid) || return 1
    kubectl exec -n default kape-smoke-killer -- sh -c '
        p=$1; ppid=$(awk "/^PPid:/{print \$2}" /proc/$p/status 2>/dev/null)
        echo "$ppid"; tr "\0" " " </proc/$ppid/cmdline 2>/dev/null' -- "$p" 2>/dev/null
}
daemon_status() { # → /status body from the daemon netns (kill pod wget)
    local p; p=$(daemon_pid) || return 1
    kubectl exec -n default kape-smoke-killer -- nsenter -t "$p" -n \
        wget -qO- "http://127.0.0.1:${DAEMON_PORT}/status" 2>/dev/null
}

# --- leg 1: create + canonical main process --------------------------------
note "leg 1: create agent with command threading"
R=$(curl -s -X POST "$BASE/agents")
ID=$(echo "$R" | jqpy 'd["id"]' 2>/dev/null) || ID=""
[ -n "$ID" ] || { echo "FAIL: create failed: $R"; exit 1; }
CREATED_IDS+=("$ID")
echo "agent $ID"

wait_status "$ID" ready "$TIMEOUT_READY" \
    && pass "agent ready (daemon reachable via #214 podIP pull path)" \
    || { fail "agent not ready in ${TIMEOUT_READY}s"; kubectl logs -n "$NS" "$CTL" --tail=30; }

POD=$(sandbox_pod "$ID")
[ -n "$POD" ] || { echo "FAIL: sandbox pod not found"; exit 1; }
echo "sandbox pod: $POD"

MPSPEC=$(kubectl get pod -n "$NS" "$POD" -o jsonpath='{.spec.containers[0].env[?(@.name=="OPENSHELL_MAIN_PROCESS_SPEC")].value}')
echo "OPENSHELL_MAIN_PROCESS_SPEC: $MPSPEC"
assert "supervisor env carries the kape-daemon main-process spec" \
    "echo '$MPSPEC' | grep -q kape-daemon"

PPID_OUT=$(daemon_ppid)
DPID=$(echo "$PPID_OUT" | head -1)
PCMD=$(echo "$PPID_OUT" | tail -n +2 | head -1)
echo "daemon host PID $DPID, parent cmdline: $PCMD"
assert "kape-daemon running with supervisor parent (canonical main process)" \
    "echo '$PCMD' | grep -q openshell-sandbox"

STATUS=$(daemon_status)
echo "daemon /status: $STATUS"
assert "daemon /status reachable with boot_id" \
    "echo '$STATUS' | jqpy 'd[\"boot_id\"]' 2>/dev/null | grep -q ."
BOOT1=$(echo "$STATUS" | jqpy 'd["boot_id"]' 2>/dev/null)

# --- leg 2: daemon death semantics ------------------------------------------
note "leg 2: daemon death"
BEFORE_POD_PHASE=$(kubectl get pod -n "$NS" "$POD" -o jsonpath='{.status.phase}')
kubectl exec -n default kape-smoke-killer -- sh -c '
    for d in /proc/[0-9]*; do
        c=$(tr "\0" " " </proc/$d/cmdline 2>/dev/null)
        case "$c" in *kape-daemon*) kill -KILL ${d#/proc/} 2>/dev/null;; esac
    done'
sleep 8
POD_PHASE=$(kubectl get pod -n "$NS" "$POD" -o jsonpath='{.status.phase}')
EXIT=$(kubectl get pod -n "$NS" "$POD" -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}')
REASON=$(kubectl get pod -n "$NS" "$POD" -o jsonpath='{.status.containerStatuses[0].state.terminated.reason}')
AGENT_STATUS=$(curl -s "$BASE/agents/$ID" | jqpy 'd["status"]' 2>/dev/null)
echo "pod phase: $BEFORE_POD_PHASE → $POD_PHASE; container exitCode=$EXIT reason=$REASON; agent status=$AGENT_STATUS"
assert "daemon death surfaces as non-zero container exit" \
    "[ -n '$EXIT' ] && [ '$EXIT' != '0' ]"
assert "sandbox CR reflects the dead sandbox (agent not ready)" \
    "[ '$AGENT_STATUS' != 'ready' ]"
kubectl get sandbox -n "$NS" -o yaml 2>/dev/null | grep -E "phase|error" | head -5 || true

# --- leg 3: pod restart re-runs the canonical command ------------------------
note "leg 3: pod restart"
kubectl delete pod -n "$NS" "$POD" --ignore-not-found >/dev/null 2>&1
NEWPOD=""
for i in $(seq 1 60); do
    NEWPOD=$(sandbox_pod "$ID")
    [ -n "$NEWPOD" ] && [ "$NEWPOD" != "$POD" ] && [ "$(kubectl get pod -n "$NS" "$NEWPOD" -o jsonpath='{.status.phase}')" = "Running" ] && break
    sleep 2
done
if [ -n "$NEWPOD" ] && [ "$NEWPOD" != "$POD" ]; then
    pass "pod recreated: $POD → $NEWPOD"
else
    NEWPOD="$POD"
    fail "pod not recreated; reusing $POD"
fi
for i in $(seq 1 60); do STATUS2=$(daemon_status) && break; sleep 2; done
BOOT2=$(echo "$STATUS2" | jqpy 'd["boot_id"]' 2>/dev/null)
echo "daemon /status after restart: $STATUS2"
assert "pod restart re-runs the daemon (new boot_id)" \
    "[ -n '$BOOT2' ] && [ '$BOOT2' != '$BOOT1' ]"
