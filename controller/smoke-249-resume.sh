#!/usr/bin/env bash
# session-pinned pi resume live smoke (issue #249, spec #245 E3): the
# controller threads KAPE_PI_SESSION_ID (derived from the agent id,
# controller/agent.go PiSessionID) into the sandbox template env at create;
# the daemon starts real pi with --session-dir /sandbox/kape/sessions (the
# openshell workspace PVC, mounted at /sandbox) + the pinned --session-id.
# Kill the pod → the replacement sandbox on the SAME PVC resumes the SAME pi
# session tree. Verified against the live microk8s gateway with the
# in-cluster smoke controller (#247 posture: daemon as canonical main process).
#
# Legs:
#   1. create: agent ready; workspace PVC exists; daemon argv carries
#      --session-dir /sandbox/kape/sessions --session-id kape-<agent suffix>;
#      pi's session tree file exists on the PVC.
#   2. real turn: prompt → agent_settled → the session file grows.
#   3. pod kill: pod deleted → replacement pod, NEW daemon boot_id, SAME PVC,
#      same --session-id, and the SAME session tree file (name + head bytes
#      preserved, line count never shrinks) — pi resumed the session.
#
# Usage: ./smoke-249-resume.sh
# Env overrides: MODEL (pi model id), PROVIDER, NS, PF_PORT, images.
set -uo pipefail

cd "$(dirname "$0")"

NS="${NS:-openshell}"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-ghcr.io/dzungtr/kape-controller:smoke-249}"
DAEMON_IMAGE="${DAEMON_IMAGE:-ghcr.io/dzungtr/kape-daemon:smoke-249}"
KILLER_IMAGE="${KILLER_IMAGE:-alpine:3.20}"
MODEL="${MODEL:-z-ai/glm-5.2}"
PROVIDER="${PROVIDER:-openrouter}"
PF_PORT="${PF_PORT:-3000}"
BASE="http://localhost:${PF_PORT}"
DAEMON_PORT="${DAEMON_PORT:-9090}"
TOKEN="smoke-249-$(head -c 12 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9')"
CTL=kape-smoke249-ctl
NOTE=/tmp/kape-smoke-249
TIMEOUT_READY=300
TIMEOUT_SETTLE=300

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
    # the k8s driver's sandbox objects go with the CR; the pods (and their
    # workspace PVCs) can outlive it — sweep the smoke's leftover pods too
    kubectl get pods -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
        | grep -- '--sbx-' | xargs -r kubectl delete pod -n "$NS" --force --grace-period=0 >/dev/null 2>&1 || true
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
    echo "=== smoke-249-resume: $PASS passed, $FAIL failed ==="
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

note "deploy in-cluster smoke controller (#247 posture: daemon = canonical main process)"
kubectl delete pod -n "$NS" "$CTL" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1
kubectl run "$CTL" -n "$NS" \
    --image="$CONTROLLER_IMAGE" --restart=Never --port=8081 \
    --overrides="$(cat <<EOF
{"spec":{"serviceAccountName":"kape-controller","containers":[{"name":"ctl","image":"$CONTROLLER_IMAGE","args":["-listen",":8081"],
  "env":[
    {"name":"KAPE_GATEWAY_ADDR","value":"openshell.openshell.svc.cluster.local:8080"},
    {"name":"KAPE_CREDS_DIR","value":"/mtls"},
    {"name":"KAPE_DEFAULT_IMAGE","value":"$DAEMON_IMAGE"},
    {"name":"KAPE_DEFAULT_PROVIDER","value":"$PROVIDER"},
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

# --- helpers ----------------------------------------------------------------
wait_status() { # id, want, timeout → 0 when reached
    local id=$1 want=$2 t=$3 i got
    for i in $(seq 1 $((t*2))); do
        got=$(curl -s "$BASE/agents/$id" | jqpy 'd["status"]' 2>/dev/null) || got=""
        [ "$got" = "$want" ] && return 0
        sleep 0.5
    done
    return 1
}
sandbox_pod() { # agent id → sandbox pod name (k8s driver names pods <ns>--<sandbox>)
    local id=$1 sbx
    sbx="sbx-${id#agent-}"
    kubectl get pods -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep -- "--${sbx}$" | head -1
}
sandbox_pvc() { # agent id → workspace PVC claim name
    local id=$1 sbx
    sbx="sbx-${id#agent-}"
    kubectl get pvc -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep "workspace-.*${sbx}" | head -1
}
session_files() { # pod → ls of the PVC session dir (via the agent container)
    kubectl exec -n "$NS" "$1" -c agent -- ls -1 /sandbox/kape/sessions 2>/dev/null
}
session_file() { # pod → the pinned session tree file name (may be a path)
    session_files "$1" | grep -E '\.jsonl$|/' | tail -1
}
session_linecount() { # pod, file → line count on the PVC
    kubectl exec -n "$NS" "$1" -c agent -- sh -c "wc -l < '/sandbox/kape/sessions/$2'" 2>/dev/null | tr -d ' '
}
session_head_md5() { # pod, file, n → md5 of the first n lines (resume identity)
    kubectl exec -n "$NS" "$1" -c agent -- sh -c "head -n '$3' '/sandbox/kape/sessions/$2' | md5sum" 2>/dev/null | cut -d' ' -f1
}
pi_session_argv() { # → the pi argv session flags seen on the host (killer pod)
    kubectl exec -n default kape-smoke-killer -- sh -c '
        for p in /host/proc/[0-9]*; do
            d=${p#/host/proc}
            c=$(tr "\0" " " </host/proc/$d/cmdline 2>/dev/null)
            case "$c" in *--session-id*) echo "$c"; exit 0;; esac
        done; exit 1' 2>/dev/null | grep -o '\-\-session-dir [^ ]* --session-id [^ ]*'
}
daemon_status() { # → /status body from the daemon netns (killer pod wget)
    local p; p=$(kubectl exec -n default kape-smoke-killer -- sh -c '
        for p in /host/proc/[0-9]*; do
            d=${p#/host/proc}
            c=$(tr "\0" " " </host/proc/$d/cmdline 2>/dev/null)
            case "$c" in *kape-daemon*) echo "$d"; exit 0;; esac
        done; exit 1' 2>/dev/null) || return 1
    kubectl exec -n default kape-smoke-killer -- nsenter -t "$p" -n \
        wget -qO- "http://127.0.0.1:${DAEMON_PORT}/status" 2>/dev/null
}

# --- leg 1: create + session-pinned argv + PVC session tree -----------------
note "leg 1: create agent, pinned session id, PVC session tree"
R=$(curl -s -X POST "$BASE/agents" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\"}")  # provider is deployment config (#227): KAPE_DEFAULT_PROVIDER
ID=$(echo "$R" | jqpy 'd["id"]' 2>/dev/null) || ID=""
[ -n "$ID" ] || { echo "FAIL: create failed: $R"; exit 1; }
CREATED_IDS+=("$ID")
WANT_SID="kape-${ID#agent-}"
echo "agent $ID (want pi session id $WANT_SID)"

wait_status "$ID" ready "$TIMEOUT_READY" \
    && pass "agent ready (daemon reachable via #214 podIP pull path)" \
    || { fail "agent not ready in ${TIMEOUT_READY}s"; kubectl logs -n "$NS" "$CTL" --tail=30; exit 1; }

POD=$(sandbox_pod "$ID")
[ -n "$POD" ] || { echo "FAIL: sandbox pod not found"; exit 1; }
echo "sandbox pod: $POD"

PVC=$(sandbox_pvc "$ID")
echo "workspace PVC: $PVC"
assert "workspace PVC bound for the sandbox (session tree home)" "[ -n '$PVC' ]"

ARGV=$(pi_session_argv)
echo "pi argv flags: $ARGV"
assert "daemon launched pi with the pinned session flags (E3)" \
    "[ '$(echo "$ARGV" | grep -o -- '--session-id [^ ]*' | cut -d' ' -f2)' = '$WANT_SID' ] && echo "$ARGV" | grep -q -- '--session-dir /sandbox/kape/sessions'"

F=$(session_file "$POD")
echo "session tree file: $F"
assert "pi session tree file exists on the PVC" "[ -n '$F' ]"
L1=$(session_linecount "$POD" "$F")
H1=$(session_head_md5 "$POD" "$F" 5)
echo "lines=$L1 head5_md5=$H1"

STATUS=$(daemon_status)
BOOT1=$(echo "$STATUS" | jqpy 'd["boot_id"]' 2>/dev/null)
echo "daemon /status: $STATUS"
assert "daemon /status reachable with boot_id" "[ -n '$BOOT1' ]"

# --- leg 2: real turn grows the session tree --------------------------------
note "leg 2: prompt turn grows the session tree"
assert "prompt accepted (202)" \
    "[ '$(http_code -X POST "$BASE/agents/$ID/prompt" -H 'Content-Type: application/json' -d '{"message":"Reply with exactly the word LIVEOK and nothing else."}')' = '202' ]"
SETTLED=0
for i in $(seq 1 $((TIMEOUT_SETTLE*2))); do
    curl -s "$BASE/agents/$ID/events?after=0&limit=1000" | grep -q '"agent_settled"' && { SETTLED=1; break; }
    sleep 0.5
done
assert "turn settled (agent_settled observed on the pull transport)" "[ '$SETTLED' = '1' ]"
L2=$(session_linecount "$POD" "$F")
echo "lines after turn: $L1 → $L2"
assert "session tree file grew after the turn" "[ -n '$L2' ] && [ '$L2' -gt '$L1' ]"

# --- leg 3: kill the pod → same PVC, same session tree resumed --------------
note "leg 3: pod kill → replacement sandbox resumes the same session tree"
kubectl delete pod -n "$NS" "$POD" --ignore-not-found >/dev/null 2>&1
NEWPOD=""
for i in $(seq 1 90); do
    NEWPOD=$(sandbox_pod "$ID")
    [ -n "$NEWPOD" ] && [ "$NEWPOD" != "$POD" ] && [ "$(kubectl get pod -n "$NS" "$NEWPOD" -o jsonpath='{.status.phase}')" = "Running" ] && break
    sleep 2
done
if [ -n "$NEWPOD" ] && [ "$NEWPOD" != "$POD" ]; then
    pass "pod replaced: $POD → $NEWPOD"
else
    NEWPOD="$POD"
    fail "pod not replaced; reusing $POD"
fi
wait_status "$ID" ready "$TIMEOUT_READY" \
    && pass "replacement sandbox ready" \
    || fail "replacement sandbox not ready in ${TIMEOUT_READY}s"

PVC2=$(sandbox_pvc "$ID")
assert "replacement sandbox uses the SAME workspace PVC" "[ -n '$PVC2' ] && [ '$PVC2' = '$PVC' ]"

STATUS2=$(daemon_status)
BOOT2=$(echo "$STATUS2" | jqpy 'd["boot_id"]' 2>/dev/null)
echo "daemon /status after replacement: $STATUS2"
assert "replacement daemon is a NEW boot (boot_id changed)" \
    "[ -n '$BOOT2' ] && [ '$BOOT2' != '$BOOT1' ]"

ARGV2=$(pi_session_argv)
echo "pi argv flags after replacement: $ARGV2"
assert "replacement daemon pins the SAME session id" \
    "[ '$(echo "$ARGV2" | grep -o -- '--session-id [^ ]*' | cut -d' ' -f2)' = '$WANT_SID' ]"

F2=$(session_file "$NEWPOD")
echo "session tree file after replacement: $F2"
assert "SAME session tree file name on the replacement sandbox" "[ '$F2' = '$F' ]"
L3=$(session_linecount "$NEWPOD" "$F")
H3=$(session_head_md5 "$NEWPOD" "$F" 5)
echo "lines=$L3 head5_md5=$H3 (before: lines=$L2 head5_md5=$H1)"
assert "session tree head bytes preserved (same tree, not a new one)" "[ -n '$H3' ] && [ '$H3' = '$H1' ]"
assert "session tree line count never shrank across the kill" "[ -n '$L3' ] && [ '$L3' -ge '$L2' ]"

echo "== smoke-249-resume legs complete: session $WANT_SID resumed across pod replacement"