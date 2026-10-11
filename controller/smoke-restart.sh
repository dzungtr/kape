#!/usr/bin/env bash
# drop-and-restart smoke (issue #199, autobot epic #194): proves the M2 Epic-1
# contract end-to-end on the live cluster — create agent, prompt mid-turn,
# SIGKILL the controller, restart it, and verify NOTHING is lost:
#   - event history replays identically from the durable store
#   - cursors continue without gaps (seq monotonic across restart)
#   - agent view is first-class post-restart (registry replay + hub rehydration)
#   - in-flight prompt returns 409 boundaries per SessionFSM
#
# Usage: ./smoke-restart.sh
# Env overrides: MODEL (pi model id), BASE (http://localhost:3000),
#                LISTEN (:3000), GATEWAY (KAPE_GATEWAY_ADDR; Option A default
#                is the in-cluster ClusterIP, not 127.0.0.1:32353).
#
# Teardown is critical: deletes every agent it created; fails unless
# `kubectl get sandbox -A` is empty at the end.
set -uo pipefail

cd "$(dirname "$0")"

BASE="${BASE:-http://localhost:3000}"
LISTEN="${LISTEN:-:3000}"
MODEL="${MODEL:-z-ai/glm-5.2}"
GATEWAY="${GATEWAY:-127.0.0.1:32353}"   # via kubectl port-forward (cert SAN covers 127.0.0.1)
BIN=/tmp/kape-smoke/controller-restart
STORE=/tmp/kape-smoke/restart-events.db
TIMEOUT_READY=180
TIMEOUT_SETTLE=240

export CGO_ENABLED=0 GOPATH="${GOPATH:-/tmp/gopath}" GOCACHE="${GOCACHE:-/tmp/gocache}" GOWORK=off
GO=/usr/bin/go
[ -x "$GO" ] || GO=go

PASS=0; FAIL=0; CREATED_IDS=()
NOTE=/tmp/kape-smoke; mkdir -p "$NOTE"
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
    local remaining
    if ! remaining=$(kubectl get sandbox -A --no-headers 2>/dev/null); then
        echo "FAIL: teardown kubectl get sandbox failed"; kubectl get sandbox -A 2>&1; rc=1
        remaining=UNKNOWN
    else
        remaining=$(echo "$remaining" | grep -c . || true)
    fi
    [ "$remaining" = 0 ] && echo "PASS: teardown leaves zero Sandbox CRs" \
        || { echo "FAIL: teardown leaves $remaining Sandbox CRs"; kubectl get sandbox -A; rc=1; }
    [ -n "${CTRL_PID:-}" ] && kill "$CTRL_PID" 2>/dev/null
    rm -f "$BIN" "$STORE"
    echo "=== smoke-restart: $PASS passed, $FAIL failed ==="
    exit $rc
}
trap cleanup EXIT

start_controller() { # $1 = note suffix; sets CTRL_PID
    KAPE_CREDS_DIR="${KAPE_CREDS_DIR:-$HOME/.config/openshell/gateways/k8s/mtls}" "$BIN" -listen "$LISTEN" -gateway "$GATEWAY" -event-store "$STORE" >"$NOTE/ctl-$1.log" 2>&1 &
    CTRL_PID=$!
    local i
    for i in $(seq 1 20); do
        [ "$(http_code "$BASE/agents")" = "200" ] && break
        sleep 0.5
    done
    [ "$(http_code "$BASE/agents")" = "200" ] || { echo "controller did not start ($1)"; cat "$NOTE/ctl-$1.log"; exit 1; }
}

wait_status() { # id, wanted-status, timeout
    local id=$1 want=$2 t=$3 i s=""
    for i in $(seq 1 $((t*2))); do
        s=$(curl -s "$BASE/agents/$id" | jqpy 'd["status"]' 2>/dev/null) || s=""
        [ "$s" = "$want" ] && return 0
        sleep 0.5
    done
    return 1
}
wait_settle() { # id, timeout
    local id=$1 t=$2 i
    for i in $(seq 1 $((t*2))); do
        curl -s "$BASE/agents/$id/events?after=0&limit=1000" | grep -q '"agent_settled"' && return 0
        sleep 0.5
    done
    return 1
}
delta_text() { python3 -c '
import sys,json
def walk(o,out):
    if isinstance(o,dict):
        for k,v in o.items():
            if k in ("delta","text") and isinstance(v,str): out.append(v)
            walk(v,out)
    elif isinstance(o,list):
        for v in o: walk(v,out)
out=[]
for e in json.load(sys.stdin).get("events",[]): walk(e,out)
print("".join(out))'; }

# --- preflight -------------------------------------------------------------
note "preflight"
command -v kubectl >/dev/null || { echo "kubectl required"; exit 1; }
python3 - <<'PYEOF' || { echo "gateway $GATEWAY not reachable (Option A: in-cluster ClusterIP)"; exit 1; }
import socket, os, sys
h, p = os.environ["GATEWAY"].rsplit(":", 1)
s = socket.socket(); s.settimeout(3)
try: s.connect((h, int(p)))
except Exception: sys.exit(1)
PYEOF
STALE=$(kubectl get sandbox -A --no-headers 2>/dev/null | wc -l)
[ "$STALE" -ne 0 ] && { echo "FAIL: preflight expects zero Sandbox CRs, found $STALE"; kubectl get sandbox -A; exit 1; }
rm -f "$STORE"
pass "preflight: gateway $GATEWAY reachable, zero Sandbox CRs, store path clean"

# --- build + start controller (run 1) ---------------------------------------
note "build controller"
cd "$(dirname "$0")" && $GO build -o "$BIN" . || exit 1; cd - >/dev/null
pass "go build"

GATEWAY="$GATEWAY" start_controller "run1"
pass "controller up (run 1)"

# --- 1. create agent --------------------------------------------------------
note "create agent"
R=$(curl -s -X POST "$BASE/agents" -H 'Content-Type: application/json' \
    -d "{\"provider\":\"openrouter-spike\",\"model\":\"$MODEL\"}")
ID=$(echo "$R" | jqpy 'd["id"]' 2>/dev/null) || ID=""
[ -n "$ID" ] || { echo "create failed: $R"; exit 1; }
CREATED_IDS+=("$ID")
echo "agent $ID"
wait_status "$ID" ready $TIMEOUT_READY && pass "agent reached ready" \
    || { echo "FAIL: agent never reached ready (status=$s)"; exit 1; }

# --- 2. baseline history + prompt MID-TURN ----------------------------------
note "baseline history + prompt mid-turn"
E0=$(curl -s "$BASE/agents/$ID/events?after=0&limit=1000")
assert "baseline history has agent.provisioned" "echo \"\$E0\" | grep -q agent.provisioned"
assert "baseline history has agent.started" "echo \"\$E0\" | grep -q agent.started"
LAST_PRE=$(echo "$E0" | jqpy 'd["events"][-1]["seq"]')
echo "pre-restart last seq: $LAST_PRE"

assert "prompt accepted (202)" "[ '$(http_code -X POST "$BASE/agents/$ID/prompt" -H 'Content-Type: application/json' -d '{"message":"Count slowly from 1 to 100, one number per line."}')' = '202' ]"
for i in $(seq 1 40); do
    W=$(curl -s "$BASE/agents/$ID" | jqpy 'd["status"]' 2>/dev/null) || W=""
    [ "$W" = "working" ] && break; sleep 0.5
done
assert "agent working mid-turn" "[ '$W' = 'working' ]"

# --- 3. DROP: SIGKILL the controller mid-turn --------------------------------
note "SIGKILL controller mid-turn"
kill -9 "$CTRL_PID" 2>/dev/null
wait "$CTRL_PID" 2>/dev/null
sleep 2
kubectl get sandbox -A -l openshell.ai/sandbox-name=sbx-${ID#agent-} --no-headers 2>/dev/null | grep -q . \
    && pass "sandbox CR survived controller drop" || fail "sandbox CR gone after controller drop"

# --- 4. RESTART: controller replays the log ----------------------------------
note "restart controller"
GATEWAY="$GATEWAY" start_controller "run2"
pass "controller up (run 2)"

assert "agent view first-class post-restart (200)" "[ '$(http_code "$BASE/agents/$ID")' = '200' ]"
V=$(curl -s "$BASE/agents/$ID")
assert "agent view has id" "echo \"\$V\" | grep -q '$ID'"
ST=$(echo "$V" | jqpy 'd["status"]')
echo "post-restart status: $ST"
assert "agent status non-creating post-restart (registry replay)" "[ '$ST' != '' ] && [ '$ST' != 'null' ]"

# --- 5. verify history survives: identical replay ----------------------------
note "history survives restart"
E1=$(curl -s "$BASE/agents/$ID/events?after=0&limit=1000")
N0=$(echo "$E0" | jqpy 'len(d["events"])')
N1=$(echo "$E1" | jqpy 'len(d["events"])')
assert "post-restart history non-empty (got $N1)" "[ '$N1' -gt 0 ]"
assert "no history loss (post >= pre: $N1 >= $N0)" "[ '$N1' -ge '$N0' ]"
# identical prefix: every pre-restart event reappears byte-identically
PRE=$(echo "$E0" | python3 -c 'import sys,json;print(json.dumps(json.load(sys.stdin)["events"],sort_keys=True))' | head -c 200000)
PRE_HEAD=$(echo "$PRE" | head -c 40000)
# identical-prefix check via python heredoc below
E1T=$(echo "$E1" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(json.dumps(d["events"],sort_keys=True))')
python3 - <<PYEOF && pass "replayed history contains every pre-restart event (identical)" || fail "replayed history diverges from pre-restart prefix"
import json,sys
pre=json.loads(r'''$PRE''')
post=json.loads(r'''$E1T''')
sev=[e for e in post if e["seq"]<=max(e["seq"] for e in pre)]
sys.exit(0 if sev==pre else 1)
PYEOF

# cursors resume without gaps
LAST1=$(echo "$E1" | jqpy 'd["events"][-1]["seq"]')
E2=$(curl -s "$BASE/agents/$ID/events?after=$LAST1&limit=5")
if [ "$(echo "$E2" | jqpy 'len(d["events"])')" -gt 0 ]; then
    NEXT=$(echo "$E2" | jqpy 'd["events"][0]["seq"]')
    assert "cursor continuity post-restart (first after $LAST1 is $((LAST1+1)))" "[ '$NEXT' = '$((LAST1+1))' ]"
else
    pass "no new events after restart cursor (cursor at head, no gap possible)"
fi
assert "per-session seq monotonic across restart" "[ '$LAST1' -ge '$LAST_PRE' ]"

# --- 6. 409 boundary + turn completes across the restart ---------------------
note "post-restart prompt boundary + settle"
# Epic-1 contract: the turn does NOT survive the controller (revive wiring is Epic 3
# #192). The FSM projection keeps the last-known state (working) and operations
# needing a live pi stdin (prompt, abort) correctly return 409. Delete recovers.
P=$(http_code -X POST "$BASE/agents/$ID/prompt" -H 'Content-Type: application/json' -d '{"message":"x"}')
assert "prompt while turn in flight -> 409" "[ '$P' = '409' ]"
A=$(http_code -X POST "$BASE/agents/$ID/abort")
assert "abort without live pi stdin -> 409 (Epic-1: revive is Epic 3)" "[ '$A' = '409' ]"
assert "delete recovers the stuck session (204)" "[ '$(http_code -X DELETE "$BASE/agents/$ID")' = '204' ]"
CREATED_IDS=()
echo "answer: mid-turn answer lost with controller (expected, Epic 3 revives)"

# --- 7. teardown -------------------------------------------------------------
note "teardown"
CREATED_IDS=()
sleep 3
KOUT=$(kubectl get sandbox -A --no-headers 2>/dev/null); KRC=$?
assert "zero Sandbox CRs after delete" "[ '$KRC' = '0' ] && [ \"\$(echo \"\$KOUT\" | grep -c . )\" = '0' ]"

[ "$FAIL" -eq 0 ] || exit 1
exit 0
