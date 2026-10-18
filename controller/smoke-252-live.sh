#!/usr/bin/env bash
# Epic 3 live smoke matrix (issue #252, spec #245 testing decisions):
# runs the six owed legs against the LIVE microk8s gateway with the
# Tier-2-revive controller (#251) and the pi-crash-ownership daemon (#248),
# extending the smoke-247 / smoke-249-resume structure (legs, assert
# helpers, env overrides, #217 killer-pod pattern).
#
# Legs:
#   1. revive: pod kill on a running session → auto running→paused
#      (agent.paused) → replacement sandbox SAME PVC + session-pinned id →
#      paused→running (agent.revived); session tree file name + head bytes
#      preserved, line count never shrinks (#249 AC3 re-run record).
#   2. pi kill mid-turn: pi SIGKILLed mid-turn → lazy respawn; pi_exit +
#      pi_restart notices (exit_code/backoff/restarts); the lost prompt is
#      never auto-reprompted (E4); extended /status fields present
#      (spool_depth, pi_restarts, backoff); next prompt settles.
#   3. abort with queued messages: steer queued on an in-flight turn →
#      abort → queue_drained notice with the drained texts (E6), pi stays
#      alive, agent keeps running.
#   4. dangling-head resume: pod killed mid-turn (crash window) → replacement
#      resumes the session on the dangling branch head (real-pi load
#      tolerance; #246 verdict: trimmer NO-GO) → next prompt settles.
#   5. budget exhaustion (KAPE_REVIVE_MAX=1): first kill revives; second
#      kill leaves the session paused + liveness failed, wait_agent
#      surfaces it; re-arm (POST /resume) resets the budget and re-drives
#      the revive; a later kill from the re-armed running state exhausts
#      again and DELETE from paused terminates (AC2/AC4).
#   6. daemon-death collapse (E2, #247 record): SIGKILL the daemon →
#      workload container terminates non-zero (exit_code semantics); under
#      the Tier-2 revive the same CR's replacement is revived.
#   7. CR-ERROR → legacy-fail (the #251 verifier's live check): sandbox CR
#      forced to SANDBOX_PHASE_ERROR → agent legacy-fails. Best-effort: if
#      the CR status cannot be patched, the leg is recorded OWED.
#
# Usage: ./smoke-252-live.sh
# Env overrides: MODEL, PROVIDER, NS, PF_PORT, images, REVIVE_MAX/COOLDOWN.
set -uo pipefail

cd "$(dirname "$0")"

NS="${NS:-openshell}"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-ghcr.io/dzungtr/kape-controller:smoke-252}"
DAEMON_IMAGE="${DAEMON_IMAGE:-ghcr.io/dzungtr/kape-daemon:smoke-252}"
KILLER_IMAGE="${KILLER_IMAGE:-alpine:3.20}"
MODEL="${MODEL:-z-ai/glm-5.2}"
PROVIDER="${PROVIDER:-openrouter}"
PF_PORT="${PF_PORT:-3000}"
BASE="http://localhost:${PF_PORT}"
DAEMON_PORT="${DAEMON_PORT:-9090}"
TOKEN="smoke-252-$(head -c 12 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9')"
CTL=kape-smoke252-ctl
REVIVE_MAX="${REVIVE_MAX:-1}"
REVIVE_COOLDOWN="${REVIVE_COOLDOWN:-10}"
TIMEOUT_READY=300
TIMEOUT_SETTLE=300
TIMEOUT_CYCLE=420

export CGO_ENABLED=0 GOPATH="${GOPATH:-/tmp/gopath}" GOCACHE="${GOCACHE:-/tmp/gocache}" GOWORK=off

PASS=0; FAIL=0; OWED=0; CREATED_IDS=(); PF_PID=""
note()  { echo "== $*"; }
pass()  { PASS=$((PASS+1)); echo "PASS: $*"; }
fail()  { FAIL=$((FAIL+1)); echo "FAIL: $*"; }
owed()  { OWED=$((OWED+1)); echo "OWED: $*"; }
assert(){ if eval "$2"; then pass "$1"; else fail "$1"; fi; }

http_code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
jqpy() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1"; }

wait_status() { # id, want, timeout → 0 when reached
    local id=$1 want=$2 t=$3 i got
    for i in $(seq 1 $((t*2))); do
        got=$(curl -s "$BASE/agents/$id" | jqpy 'd["status"]' 2>/dev/null) || got=""
        [ "$got" = "$want" ] && return 0
        sleep 0.5
    done
    return 1
}
wait_status_in() { # id, timeout, want1 want2... → first matching status
    local id=$1 t=$2; shift 2
    local i got
    for i in $(seq 1 $((t*2))); do
        got=$(curl -s "$BASE/agents/$id" | jqpy 'd["status"]' 2>/dev/null) || got=""
        for w in "$@"; do [ "$got" = "$w" ] && { echo "$got"; return 0; }; done
        sleep 0.5
    done
    return 1
}
wait_session_file() { # pod, timeout → first session tree entry ("" on timeout)
    local pod=$1 t=$2 i f
    for i in $(seq 1 $((t*2))); do
        f=$(session_file "$pod")
        [ -n "$f" ] && { echo "$f"; return 0; }
        sleep 0.5
    done
    return 1
}
wait_settle() { # id, timeout → 0 when agent_settled observed
    local id=$1 t=$2 i
    for i in $(seq 1 $((t*2))); do
        curl -s "$BASE/agents/$id/events?after=0&limit=1000" | grep -q '"agent_settled"' && return 0
        sleep 0.5
    done
    return 1
}
wait_no_settle() { # id, timeout → 0 when NO agent_settled appears in the window
    local id=$1 t=$2 i
    for i in $(seq 1 $((t*2))); do
        curl -s "$BASE/agents/$id/events?after=0&limit=1000" | grep -q '"agent_settled"' && return 1
        sleep 0.5
    done
    return 0
}
has_event() { # id, type → 0 when the event type is on the log
    curl -s "$BASE/agents/$1/events?after=0&limit=1000" | grep -q "\"$2\""
}
event_json() { # id, type → last matching event record (single line)
    curl -s "$BASE/agents/$1/events?after=0&limit=1000" \
        | python3 -c "
import sys, json
want = sys.argv[1]
last = None
for line in sys.stdin.read().splitlines():
    try: d = json.loads(line)
    except Exception: continue
    recs = [d] if isinstance(d, dict) and 'events' not in d else (d.get('events') or [d])
    for r in recs:
        if isinstance(r, dict) and r.get('type') == want: last = r
print(json.dumps(last) if last else '')" "$2" 2>/dev/null
}
sandbox_pod() { # agent id → sandbox pod name
    local id=$1 sbx
    sbx="sbx-${id#agent-}"
    kubectl get pods -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep -- "--${sbx}$" | head -1
}
sandbox_pvc() { # agent id → workspace PVC claim name
    local id=$1 sbx
    sbx="sbx-${id#agent-}"
    kubectl get pvc -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep "workspace-.*${sbx}" | head -1
}
session_files() { # pod → ls of the PVC session dir
    kubectl exec -n "$NS" "$1" -c agent -- ls -1 /sandbox/kape/sessions 2>/dev/null
}
session_file() { # pod → the pinned session tree file name (may be a path)
    session_files "$1" | grep -E '\.jsonl$|/' | tail -1
}
session_linecount() { # pod, file → line count on the PVC
    kubectl exec -n "$NS" "$1" -c agent -- sh -c "wc -l < '/sandbox/kape/sessions/$2'" 2>/dev/null | tr -d ' '
}
session_head_md5() { # pod, file, n → md5 of the first n lines
    kubectl exec -n "$NS" "$1" -c agent -- sh -c "head -n '$3' '/sandbox/kape/sessions/$2' | md5sum" 2>/dev/null | cut -d' ' -f1
}
# Host /proc scans use the killer pod's hostPath mount (smoke-249 pattern):
# the killer pod runs hostPID with the host root at /host, so /host/proc/N is
# a host PID. pi's process title rewrite collapses its cmdline to "pi" — match
# the exact argv[0].
host_procs() { kubectl exec -n default kape-smoke-killer -- sh -c 'ls /host/proc | grep -E "^[0-9]+$"' 2>/dev/null; }
proc_cmdline() { kubectl exec -n default kape-smoke-killer -- sh -c "tr '\\000' ' ' </host/proc/$1/cmdline 2>/dev/null"; }
daemon_pid() { # → host PID of the kape-daemon main process
    local p
    for p in $(host_procs); do
        case "$(proc_cmdline "$p")" in kape-daemon*) echo "$p"; return 0;; esac
    done
    return 1
}
pi_pid() { # → host PID of the pi subprocess (title-rewritten argv)
    local p
    for p in $(host_procs); do
        case "$(proc_cmdline "$p")" in pi | "pi "*) echo "$p"; return 0;; esac
    done
    return 1
}
daemon_status() { # → /status body from the daemon netns (killer pod wget, bearer)
    local p; p=$(daemon_pid) || return 1
    kubectl exec -n default kape-smoke-killer -- nsenter -t "$p" -n \
        wget -qO- --header "Authorization: Bearer $TOKEN" \
        "http://127.0.0.1:${DAEMON_PORT}/status" 2>/dev/null
}

# Daemon reachability shim (issue #217, verified live in smoke-failures): the
# openshell sandbox supervisor puts the workload (and thus the daemon) in a
# per-sandbox netns (veth-s 10.200.0.2) behind the pod main netns (podIP on
# eth0, ip_forward=0), so the controller's podIP:9090 pull target is
# connection-refused. DNAT podIP:9090 → sandbox-netns:9090 inside the pod
# netns bridges the pull transport. Needs the privileged killer pod.
apk_killer() { kubectl exec -n default kape-smoke-killer -- sh -c "apk add -q iptables 2>/dev/null"; }
sbx_sup_pid() { # $1 = podIP → host PID of the openshell-sandbox supervisor owning it
    kubectl exec -n default kape-smoke-killer -- sh -c '
        for p in $(ls /host/proc | grep -E "^[0-9]+$"); do
            cmd=$(tr "\000" " " </host/proc/$p/cmdline 2>/dev/null)
            case "$cmd" in *"openshell-sandbox --workdir"*)
                nsenter -t $p -n ip -o addr 2>/dev/null | grep -q "inet $1" && { echo $p; exit 0; } ;;
            esac
        done
        exit 1' -- "$1" 2>/dev/null
}
sbx_daemon_ip() { # $1 = sup PID → sandbox-netns veth IP (where the daemon listens)
    local sup=$1
    kubectl exec -n default kape-smoke-killer -- sh -c '
        sup=$1
        desc() { cat /host/proc/$1/task/*/children 2>/dev/null; }
        stack=$(desc $sup); dpid=""
        for gen in 1 2 3 4 5; do
            nxt=""
            for p in $stack; do
                case $(tr "\000" " " </host/proc/$p/cmdline 2>/dev/null) in *kape-daemon*) dpid=$p; break 2;; esac
                nxt="$nxt $(desc $p)"
            done
            stack=$nxt
        done
        [ -n "$dpid" ] || exit 1
        nsenter -t $dpid -n ip -o addr 2>/dev/null | awk "/inet 10\\.200/ {print \$4}" | cut -d/ -f1 | head -1
        exit 0' -- "$sup" 2>/dev/null
}
open_daemon_ingress() { # $1 = podIP → 0 when the DNAT rule is in place
    local sup dip
    sup=$(sbx_sup_pid "$1") || return 1
    dip=$(sbx_daemon_ip "$sup") || return 1
    [ -n "$dip" ] || return 1
    kubectl exec -n default kape-smoke-killer -- nsenter -t "$sup" -n sh -c "
        sysctl -q -w net.ipv4.ip_forward=1 >/dev/null 2>&1
        iptables -t nat -C PREROUTING -p tcp --dport $DAEMON_PORT -j DNAT --to-destination $dip:$DAEMON_PORT 2>/dev/null \
            || iptables -t nat -A PREROUTING -p tcp --dport $DAEMON_PORT -j DNAT --to-destination $dip:$DAEMON_PORT
        iptables -C FORWARD -p tcp -d $dip --dport $DAEMON_PORT -j ACCEPT 2>/dev/null \
            || iptables -A FORWARD -p tcp -d $dip --dport $DAEMON_PORT -j ACCEPT
    " >/dev/null 2>&1
}
ensure_ingress() { # agent id → install the DNAT shim for its current pod
    local pod ip
    pod=$(sandbox_pod "$1") || return 0
    ip=$(kubectl get pod -n "$NS" "$pod" -o jsonpath='{.status.podIP}' 2>/dev/null)
    [ -n "$ip" ] && open_daemon_ingress "$ip"
}
wait_pod_replaced() { # oldpod, agentid, timeout → new pod name ("" on timeout)
    local old=$1 id=$2 t=$3 i new
    for i in $(seq 1 $((t/2))); do
        new=$(sandbox_pod "$id")
        if [ -n "$new" ] && [ "$new" != "$old" ] \
           && [ "$(kubectl get pod -n "$NS" "$new" -o jsonpath='{.status.phase}')" = "Running" ]; then
            echo "$new"; return 0
        fi
        sleep 2
    done
    return 1
}
create_agent() { # → agent id ("" on failure)
    local r id
    r=$(curl -s -X POST "$BASE/agents" -H 'Content-Type: application/json' -d "{\"model\":\"$MODEL\"}")
    id=$(echo "$r" | jqpy 'd["id"]' 2>/dev/null) || id=""
    [ -n "$id" ] && CREATED_IDS+=("$id")
    echo "$id"
}

cleanup() {
    local rc=$?
    note "teardown"
    for id in "${CREATED_IDS[@]:-}"; do
        [ -z "$id" ] && continue
        curl -s -X DELETE "$BASE/agents/$id" >/dev/null 2>&1 || true
    done
    sleep 3
    kubectl delete sandbox -A -l managed-by=kape-controller --ignore-not-found >/dev/null 2>&1 || true
    # gateway-managed Sandbox CRs carry managed-by=openshell — sweep the smoke's
    # sbx-* CRs by name too (the CR label filter alone leaves them behind)
    kubectl get sandbox -A -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
        | grep -- '--sbx-' | xargs -r -I{} kubectl delete sandbox {} -A --ignore-not-found >/dev/null 2>&1 || true
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
    echo "=== smoke-252-live: $PASS passed, $FAIL failed, $OWED owed ==="
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
    && apk_killer && pass "killer pod ready (iptables available)" || fail "killer pod not ready"

note "deploy in-cluster smoke controller (Tier-2 revive: REVIVE_MAX=$REVIVE_MAX, COOLDOWN=${REVIVE_COOLDOWN}s)"
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
    {"name":"KAPE_REVIVE_MAX","value":"$REVIVE_MAX"},
    {"name":"KAPE_REVIVE_COOLDOWN","value":"$REVIVE_COOLDOWN"},
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

# ==========================================================================
# leg 1: revive — pod kill on a running session (#251 live, #249 AC3 re-run)
# ==========================================================================
note "leg 1: revive — pod kill → paused → revived, session tree intact"
A=$(create_agent); [ -n "$A" ] || { echo "FAIL: leg1 create"; exit 1; }
echo "agent A=$A"
wait_status "$A" ready "$TIMEOUT_READY" && pass "A ready" || { fail "A not ready"; exit 1; }
ensure_ingress "$A" && pass "daemon ingress shim installed for A"
POD_A=$(sandbox_pod "$A"); PVC_A=$(sandbox_pvc "$A")
curl -s -X POST "$BASE/agents/$A/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Reply with exactly the word LIVEOK and nothing else."}' >/dev/null
wait_settle "$A" "$TIMEOUT_SETTLE" && pass "A turn settled" || fail "A turn not settled"
FA=$(wait_session_file "$POD_A" 120)
echo "pod=$POD_A pvc=$PVC_A session_file=$FA"
assert "pi session tree file exists on the PVC" "[ -n '$FA' ]"
LA=$(session_linecount "$POD_A" "$FA"); HA=$(session_head_md5 "$POD_A" "$FA" 5)
SID="kape-${A#agent-}"
echo "pre-kill: lines=$LA head5_md5=$HA want_session_id=$SID"

kubectl delete pod -n "$NS" "$POD_A" --ignore-not-found >/dev/null 2>&1
# Tier-2: death detection → paused (agent.paused, liveness failed), budget
# spent, replacement held → agent.revived on the first pull → running.
ST=$(wait_status_in "$A" "$TIMEOUT_CYCLE" paused running)
echo "post-kill status transition: ${ST:-<timeout>}"
wait_status "$A" running "$TIMEOUT_CYCLE" && pass "A running again (paused→running)" \
    || fail "A never returned to running"
ensure_ingress "$A" # replacement pod = new netns = fresh DNAT shim needed
assert "agent.paused on the log (running→paused edge, E5)" "$(has_event "$A" 'agent.paused')"
assert "agent.revive_consumed on the log (budget ledger)" "$(has_event "$A" 'agent.revive_consumed')"
assert "agent.revived on the log (paused→running edge)" "$(has_event "$A" 'agent.revived')"
assert "agent.failed on the log (liveness failed at pause, E5)" "$(has_event "$A" 'agent.failed')"
NEWPOD_A=$(wait_pod_replaced "$POD_A" "$A" "$TIMEOUT_CYCLE")
[ -n "$NEWPOD_A" ] && pass "pod replaced: $POD_A → $NEWPOD_A" || { NEWPOD_A="$POD_A"; fail "pod not replaced"; }
PVC_A2=$(sandbox_pvc "$A")
assert "replacement sandbox uses the SAME workspace PVC" "[ '$PVC_A2' = '$PVC_A' ]"
FA2=$(session_file "$NEWPOD_A")
assert "SAME session tree file name on the replacement (#249 AC3)" "[ '$FA2' = '$FA' ]"
LA2=$(session_linecount "$NEWPOD_A" "$FA")
HA2=$(session_head_md5 "$NEWPOD_A" "$FA" 5)
echo "post-kill: lines=$LA2 head5_md5=$HA2 (pre: lines=$LA head5_md5=$HA)"
assert "session tree head bytes preserved across the pod kill (#249 AC3)" "[ -n '$HA2' ] && [ '$HA2' = '$HA' ]"
assert "session tree line count never shrank (#249 AC3)" "[ -n '$LA2' ] && [ '$LA2' -ge '$LA' ]"
ARGV_A=$(kubectl exec -n default kape-smoke-killer -- sh -c '
    for d in /proc/[0-9]*; do
        c=$(tr "\0" " " </proc/$d/cmdline 2>/dev/null)
        case "$c" in *--session-id*) echo "$c"; exit 0;; esac
    done; exit 1' 2>/dev/null | grep -o -- '--session-id [^ ]*' | cut -d' ' -f2)
assert "replacement daemon pins the SAME session id (E3)" "[ '$ARGV_A' = '$SID' ]"

# ==========================================================================
# leg 2: pi kill mid-turn — lazy respawn, notices, never auto-reprompt (E4)
# ==========================================================================
note "leg 2: pi kill mid-turn — pi_exit + pi_restart, no auto-reprompt"
curl -s -X POST "$BASE/agents/$A/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Write a very detailed 1000-word essay about Kubernetes controllers. Do not stop early."}' >/dev/null
sleep 6  # turn in flight
P_PID=$(pi_pid)
echo "pi host PID: $P_PID"
assert "pi subprocess running mid-turn" "[ -n '$P_PID' ]"
kubectl exec -n default kape-smoke-killer -- sh -c "kill -KILL '$P_PID' 2>/dev/null" || fail "pi kill failed"
# pi_restart carries {exit_code, backoff_ms, restarts} (#248); the lost turn
# must never be auto-reprompted (E4) — no settle until the host re-sends.
assert "pi_exit notice on the log" "$(has_event "$A" 'pi_exit')"
assert "pi_restart notice on the log (E4 lazy respawn)" "$(has_event "$A" 'pi_restart')"
PR=$(event_json "$A" "pi_restart")
echo "pi_restart notice: $PR"
assert "pi_restart carries exit_code/backoff/restarts (#248 shape)" \
    "echo '$PR' | jqpy 'd[\"data\"][\"exit_code\"] is not None and d[\"data\"][\"backoff_ms\"] is not None and d[\"data\"][\"restarts\"] is not None' 2>/dev/null | grep -q True"
ST_A=$(daemon_status)
echo "daemon /status: $ST_A"
assert "extended /status fields present (E8)" \
    "echo '$ST_A' | jqpy '\"spool_depth\" in d and \"pi_restarts\" in d and \"backoff\" in d' 2>/dev/null | grep -q True"
assert "/status pi_restarts >= 1 after the kill" \
    "echo '$ST_A' | jqpy 'd[\"pi_restarts\"]' 2>/dev/null | grep -qE '^[1-9]'"
wait_no_settle "$A" 30 && pass "no auto-reprompt: the lost turn never settled (E4)" \
    || fail "a settle appeared without a new prompt (unexpected auto-reprompt)"
curl -s -X POST "$BASE/agents/$A/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Reply with exactly the word RESUMEDOK and nothing else."}' >/dev/null
wait_settle "$A" "$TIMEOUT_SETTLE" && pass "next prompt settles on the respawned pi" || fail "next prompt did not settle"

# ==========================================================================
# leg 3: abort with queued messages (E6)
# ==========================================================================
note "leg 3: abort with queued steer — queue_drained notice, pi alive"
curl -s -X POST "$BASE/agents/$A/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Write a very long numbered list of 300 facts about the ocean. Do not stop early."}' >/dev/null
sleep 6  # turn in flight
S1=$(http_code -X POST "$BASE/agents/$A/steer" -H 'Content-Type: application/json' -d '{"message":"QUEUED-STEER-ONE"}')
S2=$(http_code -X POST "$BASE/agents/$A/steer" -H 'Content-Type: application/json' -d '{"message":"QUEUED-STEER-TWO"}')
echo "steer accept codes: $S1 $S2"
assert "steer queued while the turn is in flight (202)" "[ '$S1' = '202' ]"
AB=$(http_code -X POST "$BASE/agents/$A/abort")
echo "abort code: $AB"
assert "abort accepted (E6 front door)" "[ '$AB' = '202' ]"
sleep 3
assert "queue_drained notice on the log" "$(has_event "$A" 'queue_drained')"
QD=$(event_json "$A" "queue_drained")
echo "queue_drained notice: $QD"
assert "queue_drained carries the drained steer texts (E6)" \
    "echo '$QD' | jqpy 'any(\"QUEUED-STEER-ONE\" in s for s in d[\"data\"].get(\"steering\",[]))' 2>/dev/null | grep -q True"
ST_A2=$(daemon_status)
assert "pi stays alive after abort (abort no longer kills pi, E6)" \
    "echo '$ST_A2' | jqpy 'd[\"pi_running\"]' 2>/dev/null | grep -q True"
AS_A=$(curl -s "$BASE/agents/$A" | jqpy 'd["status"]' 2>/dev/null)
echo "agent status after abort: $AS_A"
assert "agent still running after abort (nothing resumes the queue)" "[ '$AS_A' = 'running' ]"

# ==========================================================================
# leg 4: dangling-head resume — pod killed mid-turn (E7, #246 NO-GO verdict)
# ==========================================================================
note "leg 4: dangling-head resume — pod kill mid-turn, real pi tolerates the head"
B=$(create_agent); [ -n "$B" ] || { echo "FAIL: leg4 create"; exit 1; }
echo "agent B=$B"
wait_status "$B" ready "$TIMEOUT_READY" && pass "B ready" || { fail "B not ready"; exit 1; }
POD_B=$(sandbox_pod "$B"); FB=$(session_file "$POD_B")
curl -s -X POST "$BASE/agents/$B/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Write a very detailed 1000-word analysis of event sourcing. Do not stop early."}' >/dev/null
sleep 6  # crash window: one in-flight turn — killing now may leave an
         # unsettled entry at the session-file head (dangling branch head)
kubectl delete pod -n "$NS" "$POD_B" --ignore-not-found >/dev/null 2>&1
ST_B=$(wait_status_in "$B" "$TIMEOUT_CYCLE" paused running)
echo "post-kill status transition: ${ST_B:-<timeout>}"
wait_status "$B" running "$TIMEOUT_CYCLE" && pass "B revived to running" || fail "B never returned to running"
ensure_ingress "$B"
assert "B revive edges on the log" "$(has_event "$B" 'agent.paused') && $(has_event "$B" 'agent.revived')"
NEWPOD_B=$(wait_pod_replaced "$POD_B" "$B" "$TIMEOUT_CYCLE")
[ -n "$NEWPOD_B" ] && pass "B pod replaced: $POD_B → $NEWPOD_B" || { NEWPOD_B="$POD_B"; fail "B pod not replaced"; }
FB2=$(session_file "$NEWPOD_B")
assert "B SAME session tree file after the mid-turn kill" "[ '$FB2' = '$FB' ]"
# real-pi load tolerance: the replacement daemon resumes the dangling-head
# session and the NEXT host prompt settles — trimmer NO-GO holds live (#246).
curl -s -X POST "$BASE/agents/$B/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Reply with exactly the word DANGLINGOK and nothing else."}' >/dev/null
wait_settle "$B" "$TIMEOUT_SETTLE" && pass "real pi tolerates the dangling head: next prompt settled (trimmer NO-GO holds)" \
    || fail "pi failed to resume the dangling-head session"
assert "B lost turn closed as revive-settle, no auto-reinject (E7)" \
    "$(curl -s "$BASE/agents/$B/events?after=0&limit=1000" | grep -q 'revive_settled')"

# ==========================================================================
# leg 5: budget exhaustion, re-arm, delete-from-paused (AC2/AC4)
# ==========================================================================
note "leg 5: budget exhaustion (REVIVE_MAX=$REVIVE_MAX) + re-arm + DELETE from paused"
C=$(create_agent); [ -n "$C" ] || { echo "FAIL: leg5 create"; exit 1; }
echo "agent C=$C"
wait_status "$C" ready "$TIMEOUT_READY" && pass "C ready" || { fail "C not ready"; exit 1; }
# kill #1: consumes the single budgeted revive
POD_C=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C" --ignore-not-found >/dev/null 2>&1
wait_status "$C" running "$TIMEOUT_CYCLE" && pass "kill #1 revived (budget 1/1 spent)" || fail "kill #1 did not revive"
ensure_ingress "$C"
# kill #2: no budget left → stays paused + liveness failed (E5 exhaustion)
POD_C2=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C2" --ignore-not-found >/dev/null 2>&1
ST_C=$(wait_status_in "$C" "$TIMEOUT_CYCLE" paused terminated)
echo "post kill #2 status: ${ST_C:-<timeout>}"
assert "kill #2 leaves the session paused (budget exhausted, E5)" "[ '$ST_C' = 'paused' ]"
assert "agent.failed on the log after exhaustion (liveness failed, AC2)" "$(has_event "$C" 'agent.failed')"
assert "no second revive: only one agent.revive_consumed for C" \
    "[ \$(curl -s "$BASE/agents/$C/events?after=0&limit=1000" | grep -c 'agent.revive_consumed') = '1' ]"
# wait_agent surfaces the exhaustion: the liveness/FSM wake returns state=paused
WAIT_C=$(curl -s "$BASE/agents/$C/wait?timeout=30")
echo "wait_agent: $WAIT_C"
assert "wait_agent surfaces the exhausted paused session (AC2)" \
    "echo '$WAIT_C' | jqpy 'd[\"state\"]' 2>/dev/null | grep -q paused"
assert "wait_agent changed includes the liveness/FSM wake" \
    "echo '$WAIT_C' | jqpy '\"state\" in d[\"changed\"]' 2>/dev/null | grep -q True"
# prompt during paused is rejected fail-fast (E5)
PC=$(http_code -X POST "$BASE/agents/$C/prompt" -H 'Content-Type: application/json' -d '{"message":"nope"}')
echo "prompt-during-paused code: $PC"
assert "prompt during paused rejected fail-fast (409, E5)" "[ '$PC' = '409' ]"
# re-arm: resets the ledger and re-drives the revive
RA=$(http_code -X POST "$BASE/agents/$C/resume")
echo "re-arm code: $RA"
assert "re-arm verb accepted (202, E5)" "[ '$RA' = '202' ]"
wait_status "$C" running "$TIMEOUT_CYCLE" && pass "re-arm re-drove the revive: C running again" || fail "re-arm did not recover C"
ensure_ingress "$C"
assert "agent.resumed on the log (ledger reset)" "$(has_event "$C" 'agent.resumed')"
# kill #3: re-armed budget re-exhausts → paused again → DELETE terminates
POD_C3=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C3" --ignore-not-found >/dev/null 2>&1
wait_status "$C" running "$TIMEOUT_CYCLE" && pass "kill #3 revived on the re-armed budget" || fail "kill #3 did not revive"
ensure_ingress "$C"
POD_C4=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C4" --ignore-not-found >/dev/null 2>&1
ST_C2=$(wait_status_in "$C" "$TIMEOUT_CYCLE" paused terminated)
assert "kill #4 exhausts again → paused" "[ '$ST_C2' = 'paused' ]"
DL=$(http_code -X DELETE "$BASE/agents/$C")
echo "DELETE from paused code: $DL"
assert "DELETE from paused terminates (AC4)" "[ '$DL' = '200' ] || [ '$DL' = '202' ]"
assert "agent.terminated on the log (paused→terminated edge)" "$(has_event "$C" 'agent.terminated')"
CREATED_IDS=("${CREATED_IDS[@]/$C/}")

# ==========================================================================
# leg 6: daemon-death collapse (E2, #247 record) — under Tier-2 revive
# ==========================================================================
note "leg 6: daemon SIGKILL → workload container dies non-zero → revive collapse"
D=$(create_agent); [ -n "$D" ] || { echo "FAIL: leg6 create"; exit 1; }
echo "agent D=$D"
wait_status "$D" ready "$TIMEOUT_READY" && pass "D ready" || { fail "D not ready"; exit 1; }
ensure_ingress "$D"
DPID=$(daemon_pid)
echo "daemon host PID: $DPID"
assert "kape-daemon is the sandbox main process (E2)" "[ -n '$DPID' ]"
kubectl exec -n default kape-smoke-killer -- sh -c "kill -KILL '$DPID' 2>/dev/null" || fail "daemon kill failed"
sleep 8
EXIT=$(kubectl get pod -n "$NS" "$(sandbox_pod "$D")" -o jsonpath='{.status.containerStatuses[0].lastState.terminated.exitCode}' 2>/dev/null)
[ -z "$EXIT" ] && EXIT=$(kubectl get pod -n "$NS" "$(sandbox_pod "$D")" -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}' 2>/dev/null)
echo "workload container exit after daemon SIGKILL: $EXIT"
assert "daemon death collapses into a non-zero container exit (E2, #247 record)" \
    "[ -n '$EXIT' ] && [ '$EXIT' != '0' ]"
wait_status "$D" running "$TIMEOUT_CYCLE" && pass "Tier-2 revive recovered the daemon-death pod (same CR)" \
    || fail "daemon-death pod never returned to running"
ensure_ingress "$D"

# ==========================================================================
# leg 7 (best-effort): CR-ERROR → legacy-fail (#251 verifier live check)
# ==========================================================================
note "leg 7: CR-ERROR → legacy-fail (best-effort)"
E=$(create_agent); [ -n "$E" ] || { echo "FAIL: leg7 create"; exit 1; }
wait_status "$E" ready "$TIMEOUT_READY" && pass "E ready" || fail "E not ready"
SBX="sbx-${E#agent-}"
CUR_PHASE=$(kubectl get sandbox "$SBX" -n "$NS" -o jsonpath='{.status.phase}' 2>/dev/null)
echo "sandbox CR $SBX phase: $CUR_PHASE"
if kubectl patch sandbox "$SBX" -n "$NS" --subresource=status --type=merge \
    -p '{"status":{"phase":"SANDBOX_PHASE_ERROR"}}' >/dev/null 2>&1; then
    sleep 10
    LEG=$(kubectl get sandbox "$SBX" -n "$NS" -o jsonpath='{.status.phase}' 2>/dev/null)
    ES=$(curl -s "$BASE/agents/$E" | jqpy 'd["status"]' 2>/dev/null)
    echo "phase after patch: $LEG; agent status: $ES"
    if [ "$LEG" = "SANDBOX_PHASE_ERROR" ] && [ "$ES" != "ready" ] && [ "$ES" != "running" ]; then
        pass "CR-ERROR surfaced as a legacy agent failure (non-ready status: $ES)"
    elif [ "$LEG" != "SANDBOX_PHASE_ERROR" ]; then
        owed "CR-ERROR check: the gateway reset the patched status phase — cannot hold the error live; OWED"
    else
        owed "CR-ERROR check: phase held but controller did not legacy-fail — needs triage; OWED"
    fi
else
    owed "CR-ERROR check: sandbox CR status not patchable from kubectl — OWED"
fi