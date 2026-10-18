# daemon smoke image profile hook (issue #217): the openshell k8s compute
# driver overrides the workload container command with the sandbox supervisor
# (main process: bash -l), so the daemon is started from a login-shell profile
# instead of the image entrypoint. Idempotent: a pidfile guard keeps nested
# login shells from double-starting. KAPE_DAEMON_ADDR must be 0.0.0.0 — the
# controller reaches the daemon on the pod IP (status.podIPs, #214).
if ! kill -0 "$(cat /tmp/kape-daemon.pid 2>/dev/null)" 2>/dev/null; then
    KAPE_DAEMON_ADDR="${KAPE_DAEMON_ADDR:-0.0.0.0:9090}"
    KAPE_SPOOL_PATH="/sandbox/kape-daemon-spool.jsonl"
    # Real-pi smoke posture (issue #249/#252): the openshell k8s compute
    # driver rebuilds the container env from the Sandbox CR spec and DROPS
    # the image ENV block (verified live run 8: pid1 env had neither
    # KAPE_PI_COMMAND nor KAPE_PI_AUTOSTART=1), so the daemon silently fell
    # back to its built-in fakepi stub. Re-assert the defaults here, in the
    # login-shell profile that actually starts the daemon; ${VAR:-default}
    # keeps any explicit env authoritative.
    KAPE_PI_COMMAND="${KAPE_PI_COMMAND:-/usr/bin/pi,--mode,rpc}"
    KAPE_PI_AUTOSTART="${KAPE_PI_AUTOSTART:-1}"
    export KAPE_DAEMON_ADDR KAPE_SPOOL_PATH KAPE_PI_COMMAND KAPE_PI_AUTOSTART
    # retried: the very first exec at container start can transiently fail
    # with EACCES (rootfs/label settling during container init, verified
    # live on microk8s, #217); copy-to-/tmp + a long backoff window makes
    # startup robust.
    (
        for i in $(seq 1 120); do
            for b in /usr/local/bin/kape-daemon /opt/openshell/bin/kape-daemon /daemon; do
                "$b" >>/tmp/kape-daemon.log 2>&1 && exit 0
                echo "kape-daemon.sh: retry $i ($b)" >>/tmp/kape-daemon.log
            done
            sleep 2
        done
    ) &
    echo $! >/tmp/kape-daemon.pid
fi
