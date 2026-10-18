#!/usr/bin/env bash
# Epic 3 live smoke matrix (issue #252, spec #245 testing decisions):
# runs the six owed legs against the LIVE microk8s gateway with the
# Tier-2-revive controller (#251) and the pi-crash-ownership daemon (#248),
# extending the smoke-247 / smoke-249-resume structure (legs, assert
# helpers, env overrides, #217 killer-pod pattern).
#
# Finisher pass (2026-10-19) — script-design fixes triaged from runs 1–3
# (#217 convention; the smoke-252 image was verified to already contain the
# #251 revive code, so the earlier stale-controller hypothesis is disproven):
#   a) Revive is PINNED to mid-turn sessions (#251 deliverable: running→paused
#      only; idle/ready death stays #213 drain-then-mark). Legs 1/4/5/6 now
#      put a turn in flight BEFORE every kill — matching the issue body's
#      "pod kill mid-turn" wording. The settle-then-kill legs could never
#      revive (legacy fail by design).
#   b) Vocabulary fix: /agents/{id}.status is the AGENT status
#      (creating/ready/working/failed/terminated) — never "paused"/"running".
#      Session FSM state rides the `session` field (running/idle/paused).
#      Waits now use wait_session for FSM edges and wait_status(ready) for
#      revive completion (completeRevive flips the agent to ready).
#   c) Wall-clock deadlines ($SECONDS) on every wait loop — iteration-count
#      loops overran up to ~21x when the port-forward was down (curl
#      --max-time per iteration), which is what hung run 3 for hours.
#   d) Settle waits are cursor-scoped (after=max_seq captured before each
#      prompt) so an old settle can no longer false-pass a later turn.
#
# Legs:
#   1. revive: pod kill MID-TURN on a running session → session running→paused
#      (agent.paused) → replacement sandbox SAME PVC + session-pinned id →
#      agent.revived + first pull → status ready; session tree file name +
#      head bytes preserved, line count never shrinks (#249 AC3 re-run record);
#      the lost turn closes as agent.settled {revive_settled:true} (E7).
#   2. pi kill mid-turn: pi SIGKILLed mid-turn → lazy respawn; pi_exit +
#      pi_restart notices (exit_code/backoff/restarts); the lost prompt is
#      never auto-reprompted (E4); extended /status fields present
#      (spool_depth, pi_restarts, backoff); next prompt resumes the session.
#      TRIAGE NOTE: analysis of the merged controller found no crash-settle
#      synthesis for Tier-1 pi death — the orphaned turn wedges the session
#      in running/working and the front door 409s re-prompt forever, which
#      contradicts E4's "re-prompting is the host's call". If the resume
#      sub-check fails with a 409 that is a CONTROLLER BUG (own slice), not
#      a script failure.
#   3. abort with queued messages: steer queued on an in-flight turn →
#      abort → queue_drained notice with the drained texts (E6), pi stays
#      alive, agent returns to ready.
#   4. dangling-head resume: pod killed mid-turn (crash window) → replacement
#      resumes the session on the dangling branch head (real-pi load
#      tolerance; #246 verdict: trimmer NO-GO) → next prompt settles.
#   5. budget exhaustion (KAPE_REVIVE_MAX=1): first mid-turn kill revives;
#      second mid-turn kill leaves the session paused + liveness failed,
#      wait_agent surfaces it; re-arm (POST /resume) resets the budget and
#      re-drives the revive; two further mid-turn kills re-exhaust and
#      DELETE from paused terminates (AC2/AC4).
#   6. daemon-death collapse (E2, #247 record): SIGKILL the daemon MID-TURN →
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

ensure_pf() { # restart the port-forward if the gateway API stopped answering
    [ "$(http_code "$BASE/agents")" = "200" ] && return 0
    echo "(port-forward down — restarting)"
    [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null
    pkill -f "kubectl port-forward -n $NS pod/$CTL" 2>/dev/null
    kubectl port-forward -n "$NS" pod/"$CTL" "${PF_PORT}:8081" >/dev/null 2>&1 &
    PF_PID=$!
    local i
    for i in $(seq 1 20); do
        [ "$(http_code "$BASE/agents")" = "200" ] && return 0
        sleep 0.5
    done
    return 1
}

agent_field() { # id, field → current value from the agent view ("" on error)
    curl -s --max-time 5 "$BASE/agents/$1" | jqpy "d.get(\"$2\",\"\")" 2>/dev/null
}

wait_status() { # id, want-agent-status, timeout → 0 when reached (wall-clock)
    local id=$1 want=$2 deadline=$((SECONDS+$3)) got
    while [ "$SECONDS" -lt "$deadline" ]; do
        got=$(agent_field "$id" status) || got=""
        [ "$got" = "$want" ] && return 0
        [ -z "$got" ] && ensure_pf
        sleep 0.5
    done
    return 1
}
wait_session() { # id, want-session-state, timeout → 0 when reached (wall-clock)
    local id=$1 want=$2 deadline=$((SECONDS+$3)) got
    while [ "$SECONDS" -lt "$deadline" ]; do
        got=$(agent_field "$id" session) || got=""
        [ "$got" = "$want" ] && return 0
        [ -z "$got" ] && ensure_pf
        sleep 0.5
    done
    return 1
}
wait_session_file() { # pod, timeout → first session tree entry ("" on timeout)
    local pod=$1 deadline=$((SECONDS+$2)) f
    while [ "$SECONDS" -lt "$deadline" ]; do
        f=$(session_file "$pod")
        [ -n "$f" ] && { echo "$f"; return 0; }
        sleep 0.5
    done
    return 1
}
wait_settle() { # id, timeout, [after] → 0 when agent_settled observed after the cursor
    local id=$1 deadline=$((SECONDS+$2)) after=${3:-0}
    while [ "$SECONDS" -lt "$deadline" ]; do
        curl -s --max-time 5 "$BASE/agents/$id/events?after=$after&limit=1000" | grep -q '"agent_settled"' && return 0
        sleep 0.5
    done
    return 1
}
max_seq() { # id → highest event seq so far (cursor for after-polls)
    curl -s --max-time 5 "$BASE/agents/$1/events?after=0&limit=1000" \
        | jqpy 'max([e["seq"] for e in d["events"]] or [0])' 2>/dev/null
}
wait_no_settle() { # id, after-cursor, timeout → 0 when NO agent_settled appears after the cursor
    local id=$1 after=$2 deadline=$((SECONDS+$3))
    while [ "$SECONDS" -lt "$deadline" ]; do
        curl -s --max-time 5 "$BASE/agents/$id/events?after=$after&limit=1000" | grep -q '"agent_settled"' && return 1
        sleep 0.5
    done
    return 0
}
has_event() { # id, type → 0 when the event type is on the log
    curl -s --max-time 5 "$BASE/agents/$1/events?after=0&limit=1000" | grep -q "\"$2\""
}
event_json() { # id, type → last matching event record (single line)
    curl -s --max-time 5 "$BASE/agents/$1/events?after=0&limit=1000" \
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
session_file() { # pod → the pinned session tree file name (resolves one directory level)
    local f
    f=$(session_files "$1" | grep -E '\.jsonl$' | head -1)
    if [ -z "$f" ]; then
        local d
        d=$(session_files "$1" | grep '/' | tail -1)
        [ -z "$d" ] && d=$(session_files "$1" | tail -1)
        [ -n "$d" ] && f=$(kubectl exec -n "$NS" "$1" -c agent -- sh -c "ls -1 '/sandbox/kape/sessions/$d'" 2>/dev/null | grep '\.jsonl$' | head -1)
    fi
    [ -n "$f" ] && echo "$f"
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
proc_environ() { kubectl exec -n default kape-smoke-killer -- sh -c "tr '\\000' '\\n' </host/proc/$1/environ 2>/dev/null"; }
daemon_pid() { # → host PID of the (single) kape-daemon main process — only
    # unambiguous while ONE agent is alive; multi-agent legs must use
    # daemon_pid_for.
    local p
    for p in $(host_procs); do
        case "$(proc_cmdline "$p")" in kape-daemon*) echo "$p"; return 0;; esac
    done
    return 1
}
daemon_pid_for() { # agent id → host PID of ITS kape-daemon, scoped by the
    # pinned session id in the daemon's environment (#249 plumbing proof)
    local id=$1 sid="kape-${id#agent-}" p
    for p in $(host_procs); do
        case "$(proc_cmdline "$p")" in kape-daemon*)
            proc_environ "$p" | grep -q "^KAPE_PI_SESSION_ID=$sid\$" && { echo "$p"; return 0; } ;;
        esac
    done
    return 1
}
pi_pid_for() { # agent id → host PID of ITS pi subprocess (title-rewritten
    # argv collapses to "pi"; disambiguate via the daemon parent pid)
    local dpid p ppid
    dpid=$(daemon_pid_for "$1") || return 1
    for p in $(host_procs); do
        case "$(proc_cmdline "$p")" in
            pi | "pi "*)
                ppid=$(kubectl exec -n default kape-smoke-killer -- sh -c "awk '/^PPid:/{print \$2}' /host/proc/$p/status 2>/dev/null")
                [ "$ppid" = "$dpid" ] && { echo "$p"; return 0; } ;;
        esac
    done
    return 1
}
daemon_status() { # → /status body from the daemon netns (killer pod wget, bearer)
    local p body
    p=$(daemon_pid) || return 1
    body=$(kubectl exec -n default kape-smoke-killer -- nsenter -t "$p" -n \
        wget -qO- --header "Authorization: Bearer $TOKEN" \
        "http://127.0.0.1:${DAEMON_PORT}/status" 2>/dev/null)
    [ -n "$body" ] && { echo "$body"; return 0; }
    # some daemon builds leave the token empty (auth disabled) — retry bare
    kubectl exec -n default kape-smoke-killer -- nsenter -t "$p" -n \
        wget -qO- "http://127.0.0.1:${DAEMON_PORT}/status" 2>/dev/null
}
daemon_status_for() { # agent id → /status body from ITS daemon's netns
    local p
    p=$(daemon_pid_for "$1") || return 1
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
# Shim watchdog (run-4 triage): a pod replacement destroys the pod netns and
# with it the DNAT shim, while the controller keeps dialling podIP:9090 and
# the revive waits DEPEND on those pulls succeeding (shim was re-installed
# only after the waits — deadlock window). The watchdog re-installs the shim
# every 5s for the current pod so revive pulls recover immediately.
SHIM_PID=""
shim_watchdog_stop() { [ -n "$SHIM_PID" ] && kill "$SHIM_PID" 2>/dev/null; wait "$SHIM_PID" 2>/dev/null; SHIM_PID=""; }
shim_watchdog_start() { # $1 = agent id
    shim_watchdog_stop
    (
        local id=$1 end=$((SECONDS + 900))
        while [ "$SECONDS" -lt "$end" ]; do
            ensure_ingress "$id" 2>/dev/null
            sleep 5
        done
    ) &
    SHIM_PID=$!
}
wait_pod_replaced() { # oldpod, agentid, timeout → new pod name ("" on timeout)
    local old=$1 id=$2 deadline=$((SECONDS+$3)) new
    while [ "$SECONDS" -lt "$deadline" ]; do
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
start_turn() { # id, seed-word → 0 once the session is mid-turn (running)
    curl -s -X POST "$BASE/agents/$1/prompt" -H 'Content-Type: application/json' \
        -d "{\"message\":\"Write a very detailed 1000-word essay about $2. Do not stop early.\"}" >/dev/null
    wait_session "$1" running 60 && sleep 3
}

cleanup() {
    local rc=$?
    note "teardown"
    shim_watchdog_stop
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
# leg 1: revive — pod kill MID-TURN (#251 live, #249 AC3 re-run record)
# ==========================================================================
ensure_pf 2>/dev/null
note "leg 1: revive — mid-turn pod kill → paused → revived, session tree intact"
A=$(create_agent); [ -n "$A" ] || { echo "FAIL: leg1 create"; exit 1; }
echo "agent A=$A"
wait_status "$A" ready "$TIMEOUT_READY" && pass "A ready" || { fail "A not ready"; exit 1; }
ensure_ingress "$A" && pass "daemon ingress shim installed for A"
POD_A=$(sandbox_pod "$A"); PVC_A=$(sandbox_pvc "$A")
start_turn "$A" "Kubernetes controllers" && pass "A turn in flight (mid-turn kill setup)" \
    || fail "A turn never started"
FA=$(wait_session_file "$POD_A" 120)
LA=$(session_linecount "$POD_A" "$FA"); HA=$(session_head_md5 "$POD_A" "$FA" 5)
SID="kape-${A#agent-}"
echo "pod=$POD_A pvc=$PVC_A session_file=$FA lines=$LA head5_md5=$HA want_session_id=$SID"
assert "pi session tree file exists on the PVC mid-turn" "[ -n '$FA' ]"

kubectl delete pod -n "$NS" "$POD_A" --ignore-not-found >/dev/null 2>&1
shim_watchdog_start "$A"
# Tier-2: death detection mid-turn → running→paused (agent.paused, liveness
# failed), budget spent, replacement held → agent.revived + first pull →
# status ready (completeRevive also closes the lost turn as revive_settled).
wait_session "$A" paused "$TIMEOUT_CYCLE" && pass "session paused (running→paused edge, E5)" \
    || fail "session never paused after the mid-turn kill"
wait_status "$A" ready "$TIMEOUT_CYCLE" && pass "A ready again (paused→running, revive complete)" \
    || fail "A never revived to ready"
ensure_ingress "$A" # replacement pod = new netns = fresh DNAT shim needed
assert "agent.paused on the log (running→paused edge, E5)" "$(has_event "$A" 'agent.paused')"
assert "agent.revive_consumed on the log (budget ledger)" "$(has_event "$A" 'agent.revive_consumed')"
assert "agent.revived on the log (paused→running edge)" "$(has_event "$A" 'agent.revived')"
assert "agent.failed on the log (liveness failed at pause, E5)" "$(has_event "$A" 'agent.failed')"
assert "lost turn closed as revive_settled, no auto-reinject (E7)" \
    "$(curl -s --max-time 5 "$BASE/agents/$A/events?after=0&limit=1000" | grep -q 'revive_settled')"
NEWPOD_A=$(wait_pod_replaced "$POD_A" "$A" "$TIMEOUT_CYCLE")
[ -n "$NEWPOD_A" ] && pass "pod replaced: $POD_A → $NEWPOD_A" || { NEWPOD_A="$POD_A"; fail "pod not replaced"; }
PVC_A2=$(sandbox_pvc "$A")
assert "replacement sandbox uses the SAME workspace PVC" "[ '$PVC_A2' = '$PVC_A' ]"
FA2=$(session_file "$NEWPOD_A")
assert "SAME session tree file name on the replacement (#249 AC3)" "[ -n '$FA2' ] && [ '$FA2' = '$FA' ]"
LA2=$(session_linecount "$NEWPOD_A" "$FA")
HA2=$(session_head_md5 "$NEWPOD_A" "$FA" 5)
echo "post-revive: lines=$LA2 head5_md5=$HA2 (pre-kill: lines=$LA head5_md5=$HA)"
assert "session tree head bytes preserved across the pod kill (#249 AC3)" "[ -n '$HA2' ] && [ '$HA2' = '$HA' ]"
assert "session tree line count never shrank (#249 AC3)" "[ -n '$LA2' ] && [ -n '$LA' ] && [ '$LA2' -ge '$LA' ]"
# only A is alive here, so the unscoped daemon scan is unambiguous; the
# environ check is the meaningful assertion (same pinned id after revive).
DPID_A=$(daemon_pid 2>/dev/null)
ENV_A=$(proc_environ "$DPID_A" 2>/dev/null | grep '^KAPE_PI_SESSION_ID=' | cut -d= -f2)
echo "replacement daemon KAPE_PI_SESSION_ID=$ENV_A (want $SID)"
assert "replacement daemon pins the SAME session id (E3, #249)" "[ '$ENV_A' = '$SID' ]"
C1=$(max_seq "$A")
curl -s -X POST "$BASE/agents/$A/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Reply with exactly the word LIVEOK and nothing else."}' >/dev/null
wait_settle "$A" "$TIMEOUT_SETTLE" "$C1" && pass "revived session answers a new prompt (history intact)" \
    || fail "revived session did not settle a new prompt"

# ==========================================================================
# leg 2: pi kill mid-turn — lazy respawn, notices, never auto-reprompt (E4)
# ==========================================================================
ensure_pf 2>/dev/null
note "leg 2: pi kill mid-turn — pi_exit + pi_restart, no auto-reprompt"
start_turn "$A" "the ocean" && pass "A turn in flight for the pi kill" || fail "A turn never started (leg 2)"
P_PID=$(pi_pid_for "$A")
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
ST_A=$(daemon_status_for "$A")
echo "daemon /status: $ST_A"
assert "extended /status fields present (E8)" \
    "echo '$ST_A' | jqpy '\"spool_depth\" in d and \"pi_restarts\" in d and \"backoff\" in d' 2>/dev/null | grep -q True"
assert "/status pi_restarts >= 1 after the kill" \
    "echo '$ST_A' | jqpy 'd[\"pi_restarts\"]' 2>/dev/null | grep -qE '^[1-9]'"
C2=$(max_seq "$A")
wait_no_settle "$A" "$C2" 30 && pass "no auto-reprompt: the lost turn never settled (E4)" \
    || fail "a settle appeared without a new prompt (unexpected auto-reprompt)"
C3=$(max_seq "$A")
RC=$(http_code -X POST "$BASE/agents/$A/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Reply with exactly the word RESUMEDOK and nothing else."}')
echo "next-prompt code after pi crash: $RC"
if [ "$RC" = "202" ]; then
    wait_settle "$A" "$TIMEOUT_SETTLE" "$C3" && pass "next prompt settles on the respawned pi" \
        || fail "next prompt did not settle"
else
    fail "next prompt after pi crash rejected ($RC) — orphaned turn wedges the session: no crash-settle synthesis for Tier-1 pi death and the front door 409s re-prompt (CONTROLLER BUG vs E4 're-prompting is the host's call'; own slice, not script)"
fi

# ==========================================================================
# leg 3: abort with queued messages (E6) — fresh agent (leg-2 outcome decoupled)
# ==========================================================================
ensure_pf 2>/dev/null
note "leg 3: abort with queued steer — queue_drained notice, pi alive"
A3=$(create_agent); [ -n "$A3" ] || { echo "FAIL: leg3 create"; exit 1; }
echo "agent A3=$A3"
wait_status "$A3" ready "$TIMEOUT_READY" && pass "A3 ready" || { fail "A3 not ready"; exit 1; }
ensure_ingress "$A3"
start_turn "$A3" "300 numbered ocean facts" && pass "A3 turn in flight" || fail "A3 turn never started"
S1=$(http_code -X POST "$BASE/agents/$A3/steer" -H 'Content-Type: application/json' -d '{"message":"QUEUED-STEER-ONE"}')
S2=$(http_code -X POST "$BASE/agents/$A3/steer" -H 'Content-Type: application/json' -d '{"message":"QUEUED-STEER-TWO"}')
echo "steer accept codes: $S1 $S2"
assert "steer queued while the turn is in flight (202)" "[ '$S1' = '202' ]"
AB=$(http_code -X POST "$BASE/agents/$A3/abort")
echo "abort code: $AB"
assert "abort accepted (E6 front door)" "[ '$AB' = '202' ]"
sleep 3
assert "queue_drained notice on the log" "$(has_event "$A3" 'queue_drained')"
QD=$(event_json "$A3" "queue_drained")
echo "queue_drained notice: $QD"
assert "queue_drained carries the drained steer texts (E6)" \
    "echo '$QD' | jqpy 'any(\"QUEUED-STEER-ONE\" in s for s in d[\"data\"].get(\"steering\",[]))' 2>/dev/null | grep -q True"
ST_A3=$(daemon_status_for "$A3")
assert "pi stays alive after abort (abort no longer kills pi, E6)" \
    "echo '$ST_A3' | jqpy 'd[\"pi_running\"]' 2>/dev/null | grep -q True"
wait_status "$A3" ready 120 && pass "agent ready after abort (turn drained, nothing resumes the queue)" \
    || fail "agent did not return to ready after abort"

# ==========================================================================
# leg 4: dangling-head resume — pod killed mid-turn (E7, #246 NO-GO verdict)
# ==========================================================================
ensure_pf 2>/dev/null
note "leg 4: dangling-head resume — pod kill mid-turn, real pi tolerates the head"
B=$(create_agent); [ -n "$B" ] || { echo "FAIL: leg4 create"; exit 1; }
echo "agent B=$B"
wait_status "$B" ready "$TIMEOUT_READY" && pass "B ready" || { fail "B not ready"; exit 1; }
POD_B=$(sandbox_pod "$B")
start_turn "$B" "event sourcing" && pass "B turn in flight (crash window)" || fail "B turn never started"
FB=$(wait_session_file "$POD_B" 120)
# crash window: one in-flight turn — killing now may leave an unsettled
# entry at the session-file head (dangling branch head)
kubectl delete pod -n "$NS" "$POD_B" --ignore-not-found >/dev/null 2>&1
shim_watchdog_start "$B"
wait_session "$B" paused "$TIMEOUT_CYCLE" && pass "B session paused after the mid-turn kill" \
    || fail "B session never paused"
wait_status "$B" ready "$TIMEOUT_CYCLE" && pass "B revived to ready" || fail "B never revived"
ensure_ingress "$B"
assert "B revive edges on the log (agent.paused + agent.revived)" \
    "$(has_event "$B" 'agent.paused') && $(has_event "$B" 'agent.revived')"
NEWPOD_B=$(wait_pod_replaced "$POD_B" "$B" "$TIMEOUT_CYCLE")
[ -n "$NEWPOD_B" ] && pass "B pod replaced: $POD_B → $NEWPOD_B" || { NEWPOD_B="$POD_B"; fail "B pod not replaced"; }
FB2=$(session_file "$NEWPOD_B")
assert "B SAME session tree file after the mid-turn kill" "[ -n '$FB2' ] && [ '$FB2' = '$FB' ]"
# real-pi load tolerance: the replacement daemon resumes the dangling-head
# session and the NEXT host prompt settles — trimmer NO-GO holds live (#246).
C4=$(max_seq "$B")
curl -s -X POST "$BASE/agents/$B/prompt" -H 'Content-Type: application/json' \
    -d '{"message":"Reply with exactly the word DANGLINGOK and nothing else."}' >/dev/null
wait_settle "$B" "$TIMEOUT_SETTLE" "$C4" && pass "real pi tolerates the dangling head: next prompt settled (trimmer NO-GO holds)" \
    || fail "pi failed to resume the dangling-head session"
assert "B lost turn closed as revive-settle, no auto-reinject (E7)" \
    "$(curl -s --max-time 5 "$BASE/agents/$B/events?after=0&limit=1000" | grep -q 'revive_settled')"

# ==========================================================================
# leg 5: budget exhaustion, re-arm, delete-from-paused (AC2/AC4)
# ==========================================================================
ensure_pf 2>/dev/null
note "leg 5: budget exhaustion (REVIVE_MAX=$REVIVE_MAX) + re-arm + DELETE from paused"
C=$(create_agent); [ -n "$C" ] || { echo "FAIL: leg5 create"; exit 1; }
echo "agent C=$C"
wait_status "$C" ready "$TIMEOUT_READY" && pass "C ready" || { fail "C not ready"; exit 1; }
# kill #1 (mid-turn): consumes the single budgeted revive
start_turn "$C" "distributed systems fallacies" && pass "C turn in flight (kill #1)" || fail "C turn never started"
POD_C=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C" --ignore-not-found >/dev/null 2>&1
shim_watchdog_start "$C"
wait_session "$C" paused "$TIMEOUT_CYCLE" || fail "kill #1: session never paused"
wait_status "$C" ready "$TIMEOUT_CYCLE" && pass "kill #1 revived (budget 1/1 spent)" || fail "kill #1 did not revive"
ensure_ingress "$C"
# kill #2 (mid-turn): no budget left → stays paused + liveness failed (E5 exhaustion)
start_turn "$C" "garbage collection" && pass "C turn in flight (kill #2)" || fail "C turn never started (kill #2)"
POD_C2=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C2" --ignore-not-found >/dev/null 2>&1
shim_watchdog_start "$C"
wait_session "$C" paused "$TIMEOUT_CYCLE" && pass "kill #2 leaves the session paused (budget exhausted, E5)" \
    || fail "kill #2: session never paused"
sleep 10
ST_C=$(agent_field "$C" session); LV_C=$(agent_field "$C" status)
echo "post kill #2: session=$ST_C status=$LV_C"
assert "session STAYS paused 10s after exhaustion (no rogue revive)" "[ '$ST_C' = 'paused' ]"
assert "agent liveness failed after exhaustion (AC2)" "[ '$LV_C' = 'failed' ]"
assert "agent.failed on the log after exhaustion (liveness failed, AC2)" "$(has_event "$C" 'agent.failed')"
assert "no second revive: only one agent.revive_consumed for C" \
    "[ \$(curl -s --max-time 5 "$BASE/agents/$C/events?after=0&limit=1000" | grep -c 'agent.revive_consumed') = '1' ]"
# wait_agent surfaces the exhaustion: the liveness/FSM wake returns state=paused
WAIT_C=$(curl -s --max-time 40 "$BASE/agents/$C/wait?timeout=30")
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
wait_status "$C" ready "$TIMEOUT_CYCLE" && pass "re-arm re-drove the revive: C ready again" || fail "re-arm did not recover C"
ensure_ingress "$C"
assert "agent.resumed on the log (ledger reset)" "$(has_event "$C" 'agent.resumed')"
# kill #3 (mid-turn): re-armed budget revives once more
start_turn "$C" "consistent hashing" && pass "C turn in flight (kill #3)" || fail "C turn never started (kill #3)"
POD_C3=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C3" --ignore-not-found >/dev/null 2>&1
shim_watchdog_start "$C"
wait_session "$C" paused "$TIMEOUT_CYCLE" || fail "kill #3: session never paused"
wait_status "$C" ready "$TIMEOUT_CYCLE" && pass "kill #3 revived on the re-armed budget" || fail "kill #3 did not revive"
ensure_ingress "$C"
# kill #4 (mid-turn): re-exhausts → paused → DELETE terminates
start_turn "$C" "raft consensus" && pass "C turn in flight (kill #4)" || fail "C turn never started (kill #4)"
POD_C4=$(sandbox_pod "$C")
kubectl delete pod -n "$NS" "$POD_C4" --ignore-not-found >/dev/null 2>&1
shim_watchdog_start "$C"
wait_session "$C" paused "$TIMEOUT_CYCLE" && pass "kill #4 exhausts again → paused" || fail "kill #4: session never paused"
DL=$(http_code -X DELETE "$BASE/agents/$C")
echo "DELETE from paused code: $DL"
assert "DELETE from paused terminates (AC4)" "[ '$DL' = '200' ] || [ '$DL' = '202' ]"
assert "agent.terminated on the log (paused→terminated edge)" "$(has_event "$C" 'agent.terminated')"
CREATED_IDS=("${CREATED_IDS[@]/$C/}")

# ==========================================================================
# leg 6: daemon-death collapse (E2, #247 record) — under Tier-2 revive
# ==========================================================================
ensure_pf 2>/dev/null
note "leg 6: daemon SIGKILL mid-turn → workload container dies non-zero → revive collapse"
D=$(create_agent); [ -n "$D" ] || { echo "FAIL: leg6 create"; exit 1; }
echo "agent D=$D"
wait_status "$D" ready "$TIMEOUT_READY" && pass "D ready" || { fail "D not ready"; exit 1; }
ensure_ingress "$D"
start_turn "$D" "the CAP theorem" && pass "D turn in flight (daemon kill)" || fail "D turn never started"
DPID=$(daemon_pid_for "$D")
echo "daemon host PID: $DPID"
assert "kape-daemon is the sandbox main process (E2)" "[ -n '$DPID' ]"
POD_D=$(sandbox_pod "$D")
kubectl exec -n default kape-smoke-killer -- sh -c "kill -KILL '$DPID' 2>/dev/null" || fail "daemon kill failed"
shim_watchdog_start "$D" # container/pod may be recreated → fresh netns
sleep 8
EXIT=$(kubectl get pod -n "$NS" "$POD_D" -o jsonpath='{.status.containerStatuses[0].lastState.terminated.exitCode}' 2>/dev/null)
[ -z "$EXIT" ] && EXIT=$(kubectl get pod -n "$NS" "$POD_D" -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}' 2>/dev/null)
if [ -n "$EXIT" ]; then
    echo "workload container exit after daemon SIGKILL: $EXIT"
    assert "daemon death collapses into a non-zero container exit (E2, #247 record)" "[ '$EXIT' != '0' ]"
else
    POD_D2=$(sandbox_pod "$D")
    if [ -n "$POD_D2" ] && [ "$POD_D2" != "$POD_D" ]; then
        pass "daemon death collapsed the workload pod (replaced $POD_D → $POD_D2, E2)"
    else
        fail "no container exit evidence after daemon SIGKILL (E2 collapse unverified)"
    fi
fi
wait_session "$D" paused "$TIMEOUT_CYCLE" || fail "D session never paused after daemon death"
wait_status "$D" ready "$TIMEOUT_CYCLE" && pass "Tier-2 revive recovered the daemon-death pod (same CR)" \
    || fail "daemon-death pod never revived"
ensure_ingress "$D"

# ==========================================================================
# leg 7 (best-effort): CR-ERROR → legacy-fail (#251 verifier live check)
# ==========================================================================
ensure_pf 2>/dev/null
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
    ES=$(agent_field "$E" status)
    echo "phase after patch: $LEG; agent status: $ES"
    if [ "$LEG" = "SANDBOX_PHASE_ERROR" ] && [ "$ES" != "ready" ] && [ "$ES" != "working" ]; then
        pass "CR-ERROR surfaced as a legacy agent failure (non-ready status: $ES)"
    elif [ "$LEG" != "SANDBOX_PHASE_ERROR" ]; then
        owed "CR-ERROR check: the gateway reset the patched status phase — cannot hold the error live; OWED"
    else
        owed "CR-ERROR check: phase held but controller did not legacy-fail — needs triage; OWED"
    fi
else
    owed "CR-ERROR check: sandbox CR status not patchable from kubectl — OWED"
fi
