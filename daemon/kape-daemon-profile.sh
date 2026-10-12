# daemon smoke image profile hook (issue #217): the openshell k8s compute
# driver overrides the workload container command with the sandbox supervisor
# (main process: bash -l), so the daemon is started from a login-shell profile
# instead of the image entrypoint. Idempotent: a pidfile guard keeps nested
# login shells from double-starting. KAPE_DAEMON_ADDR must be 0.0.0.0 — the
# controller reaches the daemon on the pod IP (status.podIPs, #214).
if [ -x /daemon ] && ! kill -0 "$(cat /tmp/kape-daemon.pid 2>/dev/null)" 2>/dev/null; then
    KAPE_DAEMON_ADDR="${KAPE_DAEMON_ADDR:-0.0.0.0:9090}"
    export KAPE_DAEMON_ADDR
    /daemon >/var/log/kape-daemon.log 2>&1 &
    echo $! >/tmp/kape-daemon.pid
fi
