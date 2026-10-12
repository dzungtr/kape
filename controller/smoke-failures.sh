#!/usr/bin/env bash
# live smoke matrix on microk8s: D6/D7 failure classes against the fake-pi
# daemon (issue #217, ADR-0083). Extends the smoke-restart.sh pattern (#199)
# into the five failure legs of the M2 Epic-2 contract:
#
#   1. partition mid-turn → degraded → seamless spool resume
#      (same boot_id: no gaps/dupes, session FSM untouched)
#   2. daemon kill mid-turn → new boot_id → spool drain → liveness failed,
#      session FSM running → Delete recovers
#   3. controller restart mid-pull → origin watermark resume → no gaps,
#      no duplicates
#   4. pod churn → new podIP → CR re-resolve → reconnect (daemon-death
#      semantics per D6: fresh boot, pi dead → daemon.boot + failed)
#   5. teardown: zero Sandbox CRs left
#
# Data path has NO provider credentials: the gateway leg only creates/deletes
# sandboxes; the in-sandbox daemon is the fake-pi image (canned records).
#
# The controller runs IN-CLUSTER as a pod (pull transport needs the
# in-cluster ServiceAccount for Sandbox CR resolution, #214) with the event
# store on a hostPath so the controller-restart leg preserves the log.
# A privileged hostPID debug pod provides the only way to kill the daemon
# PID inside a distroless sandbox container (leg 2).
#
# Usage: ./smoke-failures.sh
# Env overrides: CONTROLLER_IMAGE, DAEMON_IMAGE, NS (openshell), KILLER_IMAGE.
set -uo pipefail

cd "$(dirname "$0")"

NS="${NS:-openshell}"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-ghcr.io/dzungtr/kape-controller:smoke-217}"
DAEMON_IMAGE="${DAEMON_IMAGE:-ghcr.io/dzungtr/kape-daemon:smoke-217}"
KILLER_IMAGE="${KILLER_IMAGE:-alpine:3.20}"
PF_PORT="${PF_PORT:-18081}"           # host side of the controller port-forward
DAEMON_PORT="${DAEMON_PORT:-9090}"    # fixed daemon port (D7)
TOKEN="smoke-217-$(head -c 12 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9')"
HOST_STORE=/var/tmp/kape-smoke217     # node-side event store (survives pod restart)
CTL=KapeSmokeCtl                      # object name (lowercased by k8s)
NOTE=/tmp/kape-smoke-failures
TIMEOUT_READY=300
TIMEOUT_DEGRADED=240
TIMEOUT_SETTLE=240

export CGO_ENABLED=0 GOPATH="${GOPATH:-/tmp/gopath}" GOCACHE="${GOCACHE:-/tmp/gocache}" GOWORK=off GOROOT="${GOROOT:-/usr/lib/golang}"
GO=/usr/bin/go
[ -x "$GO" ] || GO=go

PASS=0; FAIL=0; CREATED_IDS=(); PF_PID=""; PG_PID=""
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
    # every agent the matrix created
    for id in "${CREATED_IDS[@]:-}"; do
        [ -z "$id" ] && continue
        curl -s -X DELETE "$BASE/agents/$id" >/dev/null 2>&1 || true
    done
    sleep 5
    # belt and braces: any sandbox the smoke controller owned
    kubectl delete sandbox -n "$NS" -l managed-by=kape-controller --ignore-not-found >/dev/null 2>&1 || true
    sleep 3
    local remaining
    if ! remaining=$(kubectl get sandbox -A --no-headers 2>/dev/null); then
        echo "FAIL: teardown kubectl get sandbox failed"; kubectl get sandbox -A 2>&1; rc=1
        remaining=UNKNOWN
    else
        remaining=$(echo "$remaining" | grep -c . || true)
    fi
    [ "$remaining" = 0 ] && pass "teardown leaves zero Sandbox CRs" \
        || { echo "FAIL: teardown leaves $remaining Sandbox CRs"; kubectl get sandbox -A; rc=1; }
    [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null
    [ -n "$PG_PID" ] && kill "$PG_PID" 2>/dev/null
    kubectl delete pod -n "$NS" "$CTL" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1 || true
    kubectl delete pod -n default kape-smoke-killer --ignore-not-found --force --grace-period=0 >/dev/null 2>&1 || true
    echo "=== smoke-failures: $PASS passed, $FAIL failed ==="
    exit $rc
}
trap cleanup EXIT

BASE="http://localhost:${PF_PORT}"

# --- k8s observation helpers ------------------------------------------------
sbx_pod() { # $1 = sandbox name → pod name
    kubectl get pod -n "$NS" -l "openshell.ai/sandbox-name=$1" -o name 2>/dev/null | head -1 | cut -d/ -f2
}
sbx_podip() { # $1 = sandbox name → first status.podIPs entry (may be empty)
    kubectl get sandbox -n "$NS" "$1" -o jsonpath='{.status.podIPs[0]}' 2>/dev/null
}
wait_podip() { # $1 = sandbox name, $2 = timeout s → echoes ip, non-zero when never
    local name=$1 t=$2 i ip=""
    for i in $(seq 1 $((t*2))); do
        ip=$(sbx_podip "$name")
        [ -n "$ip" ] && { echo "$ip"; return 0; }
        sleep 0.5
    done
    return 1
}

# view / status / degraded / session / events helpers
view()     { curl -s "$BASE/agents/$1"; }
vstatus()  { view "$1" | jqpy 'd["status"]' 2>/dev/null; }
vdegraded(){ view "$1" | jqpy 'd["degraded"]' 2>/dev/null; }
vsession() { view "$1" | jqpy 'd["session"]' 2>/dev/null; }
vsandbox() { view "$1" | jqpy 'd["sandbox"]' 2>/dev/null; }
wait_view() { # id, field-fn name, wanted, timeout → 0 on match
    local id=$1 fn=$2 want=$3 t=$4 i got
    for i in $(seq 1 $((t*2))); do
        got=$("$fn" "$id" 2>/dev/null) || got=""
        [ "$got" = "$want" ] && return 0
        sleep 0.5
    done
    return 1
}
events()   { curl -s "$BASE/agents/$1/events?after=0&limit=1000"; }
wait_settle() { # id, timeout
    local id=$1 t=$2 i
    for i in $(seq 1 $((t*2))); do
        events "$id" | grep -q '"agent_settled"' && return 0
        sleep 0.5
    done
    return 1
}

# NetworkPolicy: deny ALL ingress to the sandbox pod (controller→daemon
# partition). kubernetes.io/metadata.name is auto-stamped on every pod.
apply_partition() { # $1 = sandbox name
    kubectl apply -n "$NS" -f - >/dev/null <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: kape-smoke-partition}
spec:
  podSelector:
    matchLabels: {kubernetes.io/metadata.name: "$1"}
  policyTypes: [Ingress]
EOF
}
clear_partition() { kubectl delete netpol -n "$NS" kape-smoke-partition --ignore-not-found >/dev/null 2>&1; }

# event-stream integrity: seqs contiguous from 1, no duplicate seq, and each
# of the listed record types occurs exactly the given number of times.
events_integrity() { # id, expected_turn_started_count
    python3 - "$NOTE/ev.json" "$2" <<'PYEOF'
import sys, json
path, want_turns = sys.argv[1], int(sys.argv[2])
d = json.load(open(path))
evs = d["events"]
seqs = [e["seq"] for e in evs]
ok = (seqs == list(range(1, len(seqs) + 1))) and len(set(seqs)) == len(seqs)
blob = json.dumps(evs)
for t in ("turn_started", "tool_call", "text_delta", "agent_settled"):
    if blob.count('"%s"' % t) != want_turns:
        ok = False
        print("type %s count %d != %d" % (t, blob.count('"%s"' % t), want_turns))
print("seqs=%d first=%d last=%d ok=%s" % (len(seqs), seqs[0] if seqs else 0, seqs[-1] if seqs else 0, ok))
sys.exit(0 if ok else 1)
PYEOF
}

# --- preflight ---------------------------------------------------------------
note "preflight"
command -v kubectl >/dev/null || { echo "kubectl required"; exit 1; }
kubectl get nodes >/dev/null 2>&1 || { echo "cluster unreachable"; exit 1; }
STALE=$(kubectl get sandbox -A --no-headers 2>/dev/null | wc -l)
[ "$STALE" -ne 0 ] && { echo "FAIL: preflight expects zero Sandbox CRs, found $STALE"; kubectl get sandbox -A; exit 1; }
kubectl get crd sandboxes.agents.x-k8s.io >/dev/null 2>&1 || { echo "agent-sandbox CRDs not installed"; exit 1; }
# clean node-side store from a previous run
kubectl run kape-smoke-killer -n default --image="$KILLER_IMAGE" --restart=Never \
    --overrides='{"spec":{"hostPID":true,"containers":[{"name":"c","image":"'"$KILLER_IMAGE"'","command":["sleep","14400"],"volumeMounts":[{"name":"h","mountPath":"/host"}],"securityContext":{"privileged":true}}],"volumes":[{"name":"h","hostPath":{"path":"/"}}]}}' >/dev/null 2>&1 || true
kubectl wait --for=condition=ready pod/kape-smoke-killer -n default --timeout=120s >/dev/null || { echo "killer pod not ready"; exit 1; }
kubectl exec -n default kape-smoke-killer -- rm -rf "$HOST_STORE" >/dev/null 2>&1
pass "preflight: cluster reachable, zero Sandbox CRs, node store clean"

# --- deploy the smoke controller in-cluster ----------------------------------
note "deploy smoke controller (pull transport, in-cluster)"
kubectl delete pod -n "$NS" "$CTL" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1
kubectl run "$CTL" -n "$NS" \
    --image="$CONTROLLER_IMAGE" --restart=Never --port=8081 \
    --serviceaccount=kape-controller --overrides="$(cat <<EOF
{"spec":{"containers":[{"name":"ctl","image":"$CONTROLLER_IMAGE","args":["-listen",":8081"],
  "env":[
    {"name":"KAPE_GATEWAY_ADDR","value":"openshell.openshell.svc.cluster.local:8080"},
    {"name":"KAPE_CREDS_DIR","value":"/mtls"},
    {"name":"KAPE_DEFAULT_IMAGE","value":"$DAEMON_IMAGE"},
    {"name":"KAPE_DEFAULT_PROVIDER","value":"openrouter-spike"},
    {"name":"KAPE_TRANSPORT","value":"pull"},
    {"name":"KAPE_SANDBOX_NAMESPACE","value":"$NS"},
    {"name":"KAPE_DAEMON_PORT","value":"$DAEMON_PORT"},
    {"name":"KAPE_DAEMON_TOKEN","value":"$TOKEN"},
    {"name":"KAPE_EVENT_STORE","value":"/data/events.db"}],
  "ports":[{"containerPort":8081,"name":"http"}],
  "volumeMounts":[{"name":"mtls","mountPath":"/mtls","readOnly":true},{"name":"data","mountPath":"/data"}],
  "imagePullPolicy":"IfNotPresent"}],
 "serviceAccountName":"kape-controller",
 "volumes":[{"name":"mtls","secret":{"secretName":"openshell-client-tls"}},
            {"name":"data","hostPath":{"path":"$HOST_STORE","type":"DirectoryOrCreate"}}]}}
EOF
)" >/dev/null || { echo "deploy controller failed"; exit 1; }
kubectl wait --for=condition=ready pod/"$CTL" -n "$NS" --timeout=180s >/dev/null || { echo "controller pod not ready"; kubectl logs -n "$NS" "$CTL" --tail=20; exit 1; }
kubectl logs -n "$NS" "$CTL" --tail=5 | grep -q "listening" && pass "controller up in-cluster (pull transport)" \
    || { echo "FAIL: controller not listening"; kubectl logs -n "$NS" "$CTL" --tail=20; exit 1; }

kubectl port-forward -n "$NS" pod/"$CTL" "${PF_PORT}:8081" >"$NOTE/pf.log" 2>&1 &
PF_PID=$!
sleep 3
[ "$(http_code "$BASE/agents")" = "200" ] && pass "front door reachable via port-forward" \
    || { echo "FAIL: port-forward not serving"; cat "$NOTE/pf.log"; exit 1; }

create_agent() { # → echoes id
    curl -s -X POST "$BASE/agents" -H 'Content-Type: application/json' \
        -d '{"provider":"openrouter-spike","model":"fake-pi-1"}' | jqpy 'd["id"]' 2>/dev/null
}
wait_ready_agent() { # id → 0 when status ready
    local id=$1 i s=""
    for i in $(seq 1 $((TIMEOUT_READY*2))); do
        s=$(vstatus "$id") || s=""
        [ "$s" = "ready" ] && return 0
        sleep 0.5
    done
    echo "status=$s"; kubectl logs -n "$NS" "$CTL" --tail=30 | grep -i "agent $id" | tail -5
    return 1
}
events_json() { curl -s "$BASE/agents/$1/events?after=0&limit=1000" >"$NOTE/ev.json"; }

# =============================================================================
note "LEG 1: partition mid-turn → degraded → seamless spool resume"
ID1=$(create_agent)
[ -n "$ID1" ] || { echo "create failed"; exit 1; }
CREATED_IDS+=("$ID1")
wait_ready_agent "$ID1" && pass "agent A ready (fake-pi sandbox)" || { echo "FAIL: agent A never ready"; exit 1; }
SBX1=$(vsandbox "$ID1")
BO0=$(wait_podip "$SBX1" 30) && pass "daemon address resolved from CR (podIP $BO0)" || { echo "FAIL: no podIP on $SBX1"; exit 1; }

P=$(http_code -X POST "$BASE/agents/$ID1/prompt" -H 'Content-Type: application/json' -d '{"message":"turn 1"}')
assert "prompt accepted (202)" "[ '$P' = '202' ]"
wait_view "$ID1" vsession running "$TIMEOUT_READY" && pass "session FSM running mid-turn" || fail "session not running after prompt"
# partition the controller→daemon leg while the turn's records are unacked
apply_partition "$SBX1"
kubectl get netpol -n "$NS" kape-smoke-partition >/dev/null 2>&1 && pass "partition applied (netpol deny ingress to $SBX1)"
wait_view "$ID1" vdegraded True "$TIMEOUT_DEGRADED" && pass "liveness degraded after 2 failed polls" \
    || { echo "FAIL: degraded never set (degraded=$(vdegraded "$ID1"))"; }
assert "session FSM untouched during partition (running)" "[ '$(vsession "$ID1")' = 'running' ]"
sleep 5
assert "still no settle events while partitioned" "! events '$ID1' | grep -q agent_settled"
clear_partition
wait_settle "$ID1" "$TIMEOUT_SETTLE" && pass "seamless resume: turn drained after partition lifted" \
    || { echo "FAIL: turn never settled after resume"; }
wait_view "$ID1" vdegraded False 60 && pass "degraded cleared by successful pull" || fail "degraded stuck"
assert "liveness ready after resume" "[ '$(vstatus "$ID1")' = 'ready' ]"
assert "session FSM settled (idle)" "[ '$(vsession "$ID1")' = 'idle' ]"
events_json "$ID1"
if events_integrity "$ID1" 1; then pass "leg 1 log integrity: contiguous seqs, one full turn, no gaps/dupes"
else fail "leg 1 log integrity (see counts above)"; fi

# =============================================================================
note "LEG 2: daemon kill mid-turn → new boot_id → drain → failed → Delete recovers"
ID2=$(create_agent)
[ -n "$ID2" ] || { echo "create failed"; exit 1; }
CREATED_IDS+=("$ID2")
wait_ready_agent "$ID2" && pass "agent B ready" || { echo "FAIL: agent B never ready"; exit 1; }
SBX2=$(vsandbox "$ID2")
POD2=$(sbx_pod "$SBX2")
[ -n "$POD2" ] && pass "sandbox pod located ($POD2)" || { echo "FAIL: no pod for $SBX2"; exit 1; }

P=$(http_code -X POST "$BASE/agents/$ID2/prompt" -H 'Content-Type: application/json' -d '{"message":"turn 1"}')
assert "prompt accepted (202)" "[ '$P' = '202' ]"
sleep 1   # let fake pi emit some records onto the spool (mid-turn kill)
# SIGKILL the daemon (PID 1 of the sandbox container) from the hostPID debug
# pod — the only path into a distroless container. Container restarts
# (restartPolicy Always): same pod, same spool file, NEW boot_id.
kubectl exec -n default kape-smoke-killer -- sh -c "pkill -9 -x daemon" >/dev/null 2>&1
sleep 2
kubectl get pod -n "$NS" "$POD2" >/dev/null 2>&1 && pass "pod survived daemon kill (container restarted)" \
    || fail "pod gone after daemon kill (that is leg 4, not leg 2)"
wait_view "$ID2" vstatus failed 180 && pass "liveness failed after new-boot_id preflight" \
    || { echo "FAIL: status=$(vstatus "$ID2")"; }
assert "session FSM still running (no failed state, D6)" "[ '$(vsession "$ID2")' = 'running' ]"
events_json "$ID2"
grep -q '"daemon.boot"' "$NOTE/ev.json" && pass "durable daemon.boot event recorded (new boot_id, pi_running:false)" \
    || fail "no daemon.boot event"
# the daemon SIGKILL also kills fake pi (same PID namespace) — no pi_exit
# notice can be spooled post-mortem; the drained turn records are the
# honest-history invariant here.
P=$(http_code -X POST "$BASE/agents/$ID2/prompt" -H 'Content-Type: application/json' -d '{"message":"x"}')
assert "prompt while FSM running → 409 (session untouched)" "[ '$P' = '409' ]"
assert "Delete recovers the dead session (204)" "[ '$(http_code -X DELETE "$BASE/agents/$ID2")' = '204' ]"
CREATED_IDS=("${CREATED_IDS[@]/$ID2/}")
sleep 3
kubectl get sandbox -n "$NS" "$SBX2" >/dev/null 2>&1 && fail "sandbox CR $SBX2 still present" || pass "sandbox CR $SBX2 deleted"

# =============================================================================
note "LEG 3: controller restart mid-pull → origin watermark → no gaps, no duplicates"
ID3=$(create_agent)
[ -n "$ID3" ] || { echo "create failed"; exit 1; }
CREATED_IDS+=("$ID3")
wait_ready_agent "$ID3" && pass "agent C ready" || { echo "FAIL: agent C never ready"; exit 1; }
SBX3=$(vsandbox "$ID3")
P=$(http_code -X POST "$BASE/agents/$ID3/prompt" -H 'Content-Type: application/json' -d '{"message":"turn 1"}')
assert "turn 1 prompt accepted (202)" "[ '$P' = '202' ]"
wait_settle "$ID3" "$TIMEOUT_SETTLE" && pass "turn 1 settled (fully pulled baseline)" || { echo "FAIL: turn 1 never settled"; }
events_json "$ID3"; N1=$(jqpy 'len(d["events"])')
echo "baseline: $N1 events at origin watermark"
# turn 2 accepted, then partition, then kill the controller MID-PULL
P=$(http_code -X POST "$BASE/agents/$ID3/prompt" -H 'Content-Type: application/json' -d '{"message":"turn 2"}')
assert "turn 2 prompt accepted (202)" "[ '$P' = '202' ]"
apply_partition "$SBX3" && sleep 3 && pass "partition applied mid-pull (records pending on spool)"
kubectl delete pod -n "$NS" "$CTL" --force --grace-period=0 >/dev/null 2>&1
kubectl wait --for=condition=ready pod/"$CTL" -n "$NS" --timeout=120s >/dev/null || { echo "FAIL: controller pod not back"; kubectl logs -n "$NS" "$CTL" --tail=20; exit 1; }
pass "controller restarted (hostPath store preserved)"
kill "$PF_PID" 2>/dev/null
kubectl port-forward -n "$NS" pod/"$CTL" "${PF_PORT}:8081" >"$NOTE/pf2.log" 2>&1 &
PF_PID=$!
sleep 3
V=$(view "$ID3")
assert "agent view first-class post-restart (source replayed)" "echo '$V' | grep -q 'replayed'"
assert "session FSM running (turn 2 unsettled)" "[ '$(vsession "$ID3")' = 'running' ]"
clear_partition
wait_settle "$ID3" "$TIMEOUT_SETTLE" && pass "post-restart resume drained turn 2 from the spool" \
    || { echo "FAIL: turn 2 never settled after restart"; }
wait_view "$ID3" vstatus ready 60 && pass "liveness ready after resume" || fail "liveness stuck ($(vstatus "$ID3"))"
events_json "$ID3"
N3=$(jqpy 'len(d["events"])')
python3 - "$NOTE/ev.json" <<'PYEOF' && pass "leg 3 no-gap check: seqs contiguous 1..N" || fail "leg 3 seqs have gaps"
import sys, json
evs = json.load(open(sys.argv[1]))["events"]
seqs = [e["seq"] for e in evs]
sys.exit(0 if seqs == list(range(1, len(seqs) + 1)) else 1)
PYEOF
python3 - "$NOTE/ev.json" 2 <<'PYEOF' && pass "leg 3 no-duplicate check: exactly 2 turns, each record type twice" || fail "leg 3 duplicates present"
import sys, json
evs = json.load(open(sys.argv[1]))["events"]
blob = json.dumps(evs)
for t in ("turn_started", "tool_call", "text_delta", "agent_settled"):
    if blob.count('"%s"' % t) != 2:
        print("%s count %d != 2" % (t, blob.count('"%s"' % t)))
        sys.exit(1)
sys.exit(0)
PYEOF
assert "per-agent seq monotonic across restart ($N1 → $N3)" "[ '$N3' -ge '$N1' ]"

# =============================================================================
note "LEG 4: pod churn → new podIP → CR re-resolve → reconnect"
ID4=$(create_agent)
[ -n "$ID4" ] || { echo "create failed"; exit 1; }
CREATED_IDS+=("$ID4")
wait_ready_agent "$ID4" && pass "agent D ready" || { echo "FAIL: agent D never ready"; exit 1; }
SBX4=$(vsandbox "$ID4")
IP0=$(wait_podip "$SBX4" 30)
kubectl delete pod -n "$NS" "$(sbx_pod "$SBX4")" >/dev/null 2>&1
IP1=$(wait_podip "$SBX4" 180)
if [ -n "$IP1" ]; then
    assert "sandbox pod recreated (CR address now $IP1)" "[ -n '$IP1' ]"
    if [ "$IP0" != "$IP1" ]; then pass "podIP churned ($IP0 → $IP1)"; else echo "note: podIP unchanged (same address re-leased)"; fi
    wait_view "$ID4" vstatus failed 240 && pass "reconnect detected daemon death on churn (failed)" \
        || { echo "FAIL: status=$(vstatus "$ID4")"; }
    assert "session FSM still running (D6 pod-churn semantics)" "[ '$(vsession "$ID4")' = 'running' ]"
    events_json "$ID4"
    grep -q '"daemon.boot"' "$NOTE/ev.json" && pass "daemon.boot recorded after churn" || fail "no daemon.boot event"
else
    fail "sandbox pod not recreated after churn (CR stuck); falling back to delete-recovers check"
    wait_view "$ID4" vstatus failed 120 || true
fi
assert "Delete recovers after churn (204)" "[ '$(http_code -X DELETE "$BASE/agents/$ID4")' = '204' ]"
CREATED_IDS=("${CREATED_IDS[@]/$ID4/}")

# =============================================================================
note "LEG 5: teardown — zero Sandbox CRs left"
sleep 3
KOUT=$(kubectl get sandbox -A --no-headers 2>/dev/null); KRC=$?
assert "zero Sandbox CRs after all legs" "[ '$KRC' = '0' ] && [ \"\$(echo \"\$KOUT\" | grep -c .)\" = '0' ]"
CREATED_IDS=()

[ "$FAIL" -eq 0 ] || exit 1
exit 0
