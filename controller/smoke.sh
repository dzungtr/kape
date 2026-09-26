#!/usr/bin/env bash
# controller v1 live-cluster smoke (issue #179): drives the full v1 flow
# against the live OpenShell gateway — build controller, run on :3000
# (nono denies 8081), create agent (default profile + one overridden),
# subscribe SSE, send prompt, read_events via cursor pull incl. type filter,
# abort, list, delete — asserting Sandbox CRs exist during and zero after.
#
# Usage: ./smoke.sh
# Env overrides: MODEL (pi model id, default z-ai/glm-5.2 used live in #177),
# LISTEN (:3000), BASE (http://localhost:3000).
#
# Teardown is critical: the trap deletes any agent created here; the script
# fails unless `kubectl get sandbox -A` is empty at the end.
set -uo pipefail

cd "$(dirname "$0")"

BASE="${BASE:-http://localhost:3000}"
LISTEN="${LISTEN:-:3000}"
MODEL="${MODEL:-z-ai/glm-5.2}"
BIN=/tmp/kape-smoke/controller
TIMEOUT_READY=180
TIMEOUT_SETTLE=180

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
    # belt and braces: delete any sandbox carrying the controller ownership label
    kubectl delete sandbox -A -l managed-by=kape-controller --ignore-not-found >/dev/null 2>&1 || true
    sleep 2
    local remaining
    if ! remaining=$(kubectl get sandbox -A --no-headers 2>/dev/null); then
        echo "FAIL: teardown kubectl get sandbox failed — cannot verify zero CRs"; kubectl get sandbox -A 2>&1; rc=1
        remaining=UNKNOWN
    else
        remaining=$(echo "$remaining" | grep -c . || true)
    fi
    if [ "$remaining" = 0 ]; then
        echo "PASS: teardown leaves zero Sandbox CRs"
    else
        echo "FAIL: teardown leaves $remaining Sandbox CRs:"; kubectl get sandbox -A; rc=1
    fi
    [ -n "${CTRL_PID:-}" ] && kill "$CTRL_PID" 2>/dev/null
    rm -f "$BIN"
    echo "=== smoke: $PASS passed, $FAIL failed ==="
    exit $rc
}
trap cleanup EXIT

# --- preflight -------------------------------------------------------------
note "preflight"
command -v kubectl >/dev/null || { echo "kubectl required"; exit 1; }
command -v nc >/dev/null || { echo "nc required"; exit 1; }
nc -z 127.0.0.1 32353 || { echo "gateway 127.0.0.1:32353 not reachable"; exit 1; }
kubectl get provider -A 2>/dev/null | grep -q openrouter-spike \
    || openshell provider list 2>/dev/null | grep -q openrouter-spike \
    || { echo "provider openrouter-spike missing"; exit 1; }
STALE=$(kubectl get sandbox -A --no-headers 2>/dev/null | wc -l)
[ "$STALE" -ne 0 ] && { echo "FAIL: preflight expects zero Sandbox CRs, found $STALE:"; kubectl get sandbox -A; exit 1; }
pass "preflight: gateway reachable, provider present, zero Sandbox CRs"

# --- build + run -----------------------------------------------------------
note "build controller"
$GO build -o "$BIN" . || exit 1
pass "go build"

note "start controller on $LISTEN"
"$BIN" -listen "$LISTEN" >"$NOTE/controller.log" 2>&1 &
CTRL_PID=$!
for i in $(seq 1 20); do
    [ "$(http_code "$BASE/agents")" = "200" ] && break
    sleep 0.5
done
[ "$(http_code "$BASE/agents")" = "200" ] || { echo "controller did not start"; cat "$NOTE/controller.log"; exit 1; }
pass "controller up on $BASE"

# --- 1. create agent, default profile --------------------------------------
note "create agent (default profile)"
R=$(curl -s -X POST "$BASE/agents")
ID1=$(echo "$R" | jqpy 'd["id"]' 2>/dev/null) || ID1=""
[ -n "$ID1" ] || { echo "create failed: $R"; exit 1; }
CREATED_IDS+=("$ID1")
echo "agent $ID1"
CR_COUNT=0
for i in $(seq 1 10); do
    # user labels live in the gateway's own metadata store, not on the k8s
    # Sandbox CR (verified live for #179) — select the CR by its system label
    CR_COUNT=$(kubectl get sandbox -A -l openshell.ai/sandbox-name=sbx-${ID1#agent-} --no-headers 2>/dev/null | wc -l)
    [ "$CR_COUNT" -gt 0 ] && break
    sleep 1
done
assert "sandbox CR exists during creation (found $CR_COUNT)" "[ '$CR_COUNT' -gt 0 ]"

wait_status() { # id, wanted-status, timeout
    local id=$1 want=$2 t=$3 i
    for i in $(seq 1 $((t*2))); do
        s=$(curl -s "$BASE/agents/$id" | jqpy 'd["status"]' 2>/dev/null) || s=""
        [ "$s" = "$want" ] && return 0
        sleep 0.5
    done
    return 1
}
wait_status "$ID1" ready $TIMEOUT_READY && pass "agent $ID1 reached ready" \
    || { echo "FAIL: agent $ID1 never reached ready (status=$s)"; exit 1; }

# --- 2. create agent, overridden profile ------------------------------------
note "create agent (overridden profile)"
R=$(curl -s -X POST "$BASE/agents" -H 'Content-Type: application/json' \
    -d "{\"name\":\"smoke-override\",\"resources\":{\"cpu\":\"500m\",\"memory\":\"1Gi\"},\"provider\":\"openrouter-spike\",\"model\":\"$MODEL\"}")
ID2=$(echo "$R" | jqpy 'd["id"]' 2>/dev/null) || ID2=""
[ -n "$ID2" ] || { echo "create (override) failed: $R"; exit 1; }
CREATED_IDS+=("$ID2")
echo "agent $ID2"
V=$(curl -s "$BASE/agents/$ID2")
assert "override sandbox named smoke-override" "echo \"\$V\" | grep -q smoke-override"
wait_status "$ID2" ready $TIMEOUT_READY && pass "agent $ID2 reached ready" \
    || { echo "FAIL: agent $ID2 never reached ready (status=$s)"; exit 1; }

# --- 3. subscribe SSE + prompt default agent --------------------------------
note "SSE + prompt round trip (default agent $ID1)"
curl -sN "$BASE/agents/$ID1/events" >"$NOTE/sse-$ID1.log" &
SSE_PID=$!

assert "prompt accepted (202)" "[ '$(http_code -X POST "$BASE/agents/$ID1/prompt" -H 'Content-Type: application/json' -d '{"message":"Reply with exactly the word LIVEOK and nothing else."}')' = '202' ]"
W=""; for i in $(seq 1 20); do
    W=$(curl -s "$BASE/agents/$ID1" | jqpy 'd["status"]' 2>/dev/null) || W=""
    [ "$W" = "working" ] && break; sleep 0.5
done
assert "agent status working during turn" "[ '$W' = 'working' ]"
assert "second prompt rejected (409)" "[ '$(http_code -X POST "$BASE/agents/$ID1/prompt" -H 'Content-Type: application/json' -d '{"message":"x"}')' = '409' ]"

wait_settle() { # id, timeout — poll read_events for agent_settled
    local id=$1 t=$2 i
    for i in $(seq 1 $((t*2))); do
        curl -s "$BASE/agents/$id/events?after=0&limit=1000" | grep -q '"agent_settled"' && return 0
        sleep 0.5
    done
    return 1
}
wait_settle "$ID1" $TIMEOUT_SETTLE && pass "turn settled (agent_settled observed)" \
    || { echo "FAIL: turn never settled"; exit 1; }
grep -q 'text_delta' "$NOTE/sse-$ID1.log" && pass "SSE observed text_delta events (live round trip)" \
    || fail "SSE observed no text_delta events"
assert "SSE observed agent_settled" "grep -q agent_settled $NOTE/sse-$ID1.log"
# concatenate streamed answer text: live pi wraps deltas as message_update
# records with assistantMessageEvent{type:text_delta,delta:...}; collect all
# "delta"/"text" strings recursively from the pull batch
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
ANS=$(curl -s "$BASE/agents/$ID1/events?after=0&limit=1000" | delta_text)
echo "answer: $ANS"
assert "turn answered via pull (deltas)" "echo "\$ANS" | grep -q LIVEOK"
assert "pull batch contains agent_settled" "curl -s "$BASE/agents/$ID1/events?after=0&limit=1000" | grep -q agent_settled"
SANS=$(python3 -c "import json
out=[]
def walk(o):
    if isinstance(o,dict):
        for k,v in o.items():
            if k in ('delta','text') and isinstance(v,str): out.append(v)
            walk(v)
    elif isinstance(o,list):
        for v in o: walk(v)
for line in open('$NOTE/sse-$ID1.log'):
    line=line.strip()
    if line.startswith('data:'):
        try: walk(json.loads(line[5:]))
        except Exception: pass
print(''.join(out))")
assert "turn answered via SSE (deltas)" "echo "\$SANS" | grep -q LIVEOK"
sleep 1; kill $SSE_PID 2>/dev/null

# --- 4. read_events cursor pull + type filter --------------------------------
note "read_events cursor pull incl. type filter"
E=$(curl -s "$BASE/agents/$ID1/events?after=0&limit=1000")
assert "pull response has agent_id" "echo \"\$E\" | grep -q '\"agent_id\":\"$ID1\"'"
assert "pull response has truncated flag" "echo \"\$E\" | grep -q '\"truncated\"'"
FIRST=$(echo "$E" | jqpy 'd["events"][0]["seq"]')
# no gaps/dups: advance the cursor by one and check the batch starts at seq+1
E2=$(curl -s "$BASE/agents/$ID1/events?after=$FIRST&limit=5")
NEXT=$(echo "$E2" | jqpy 'd["events"][0]["seq"]')
assert "cursor continuity (first after $FIRST is $NEXT)" "[ '$NEXT' = '$((FIRST+1))' ]"
# type filter: only lifecycle events
EF=$(curl -s "$BASE/agents/$ID1/events?after=0&limit=1000&types=agent.provisioned,agent.started")
ONLY=$(echo "$EF" | jqpy 'all(e["type"] in ("agent.provisioned","agent.started") for e in d["events"])')
assert "types filter returns only requested lifecycle events" "[ '$ONLY' = 'True' ]"
NPROC=$(echo "$EF" | jqpy 'len(d["events"])')
assert "types filter found both lifecycle events (got $NPROC)" "[ '$NPROC' = '2' ]"
# delta exclusion via types (deltas excluded when filtered out)
ND=$(echo "$EF" | jqpy 'sum(1 for e in d["events"] if e["type"]=="text_delta")')
assert "type-filtered pull excludes text_delta" "[ '$ND' = '0' ]"

# --- 5. prompt + abort the overridden agent ----------------------------------
note "prompt + abort (overridden agent $ID2)"
curl -s -X POST "$BASE/agents/$ID2/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Count slowly from 1 to 100, one number per line."}' >/dev/null
for i in $(seq 1 60); do
    [ "$(curl -s "$BASE/agents/$ID2" | jqpy 'd["status"]')" = "working" ] && break; sleep 0.5
done
A=$(http_code -X POST "$BASE/agents/$ID2/abort")
assert "abort accepted (202)" "[ '$A' = '202' ]"
wait_status "$ID2" ready $TIMEOUT_SETTLE && pass "agent $ID2 stable (ready) after abort" \
    || fail "agent $ID2 did not reach ready after abort (status=$s)"

# --- 6. list -----------------------------------------------------------------
note "list_agents"
L=$(curl -s "$BASE/agents")
N=$(echo "$L" | jqpy 'len(d)')
assert "list shows both agents (got $N)" "[ '$N' = '2' ]"
assert "list marks agents live" "echo \"\$L\" | grep -q '\"live\"'"

# --- 7. delete ---------------------------------------------------------------
note "delete + teardown assertions"
assert "delete agent $ID1 (204)" "[ '$(http_code -X DELETE "$BASE/agents/$ID1")' = '204' ]"
assert "delete agent $ID2 (204)" "[ '$(http_code -X DELETE "$BASE/agents/$ID2")' = '204' ]"
CREATED_IDS=()
assert "get after delete is 404" "[ '$(http_code "$BASE/agents/$ID1")' = '404' ]"
assert "list empty after delete" "[ \"\$(curl -s "$BASE/agents" | jqpy 'len(d)')\" = '0' ]"
sleep 3
KOUT=$(kubectl get sandbox -A --no-headers 2>/dev/null); KRC=$?
assert "zero Sandbox CRs after delete" "[ '$KRC' = '0' ] && [ \"\$(echo \"\$KOUT\" | grep -c . )\" = '0' ]"

[ "$FAIL" -eq 0 ] || exit 1
exit 0
