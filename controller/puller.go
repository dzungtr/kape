package main

import (
	"context"
	"log"
	"time"
)

// Puller — the per-agent daemon pull loop (issue #212, ADR-0083 D1/D2/D6):
// long-polls GET /records?after=<ack cursor>&wait=25s and appends verbatim
// records into the agent's EventHub. The ack cursor is the store-side origin
// watermark (the highest spool_id durably appended), so a controller restart
// resumes exactly where the log ends: no gaps, no duplicates. The poll is
// the heartbeat; the empty steady-state poll is one request per wait.
//
// The daemon never assigns client-facing seqs — the hub stays the seq
// authority; the spool_id is stored as an internal sidecar on each pulled
// event only (the client-facing {seq,type,data} envelope is unchanged).

// daemonPullWait is the long-poll wait (D2/D6): steady-state heartbeat
// ≈ one empty poll / 25s.
const daemonPullWait = 25 * time.Second

// puller backoff on failures (D6): 1s doubling to a 30s cap.
const (
	pullBackoffBase = 1 * time.Second
	pullBackoffCap  = 30 * time.Second
)

// degradedFailureThreshold is the number of consecutive pull failures that
// flips the transient agent_view `degraded` flag (issue #213, D6); any
// successful pull clears it.
const degradedFailureThreshold = 2

// daemonBootEventType is the durable, non-FSM lifecycle event appended when
// the reconnect preflight detects daemon death (new boot_id + pi_running:false,
// issue #213, D6). It is ignored by the session projection (fsm.go) and by
// the agent-status replay fold (registry.go: agent.failed carries the
// liveness), so replaying the log lands on identical state.
const daemonBootEventType = "daemon.boot"

// runPuller drives one agent's pull loop until ctx is cancelled or daemon
// death is detected. after starts at the hub's recovered origin watermark so
// restarts never re-append.
func (m *AgentManager) runPuller(agent *Agent, ctx context.Context) {
	// Learn the initial boot identity (best-effort): without it the first
	// preflight cannot distinguish partition from death.
	m.noteBootID(agent, ctx) // fallback: Create already learned it synchronously
	backoff := pullBackoffBase
	failures := 0
	pendingDeath := false
	for {
		// Pending daemon death: drain the spool (partial lost-turn records =
		// honest history), append daemon.boot, then mark failed. Retried on
		// each cycle until the drain completes or ctx ends.
		if pendingDeath {
			if !sleepCtx(ctx, backoff) {
				return
			}
			st, err := m.preflight(agent, ctx)
			if err != nil {
				continue
			}
			if m.drainSpool(agent, ctx) {
				m.recordDaemonBoot(agent, st)
				m.fail(agent, "daemon restarted (new boot_id) and pi not running")
				return // recovery = Delete → terminated (no failed session state)
			}
			continue // drain failed; retry next cycle
		}
		after := agent.hub.Origin()
		recs, err := agent.daemon.Records(ctx, after, daemonPullWait)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			failures++
			if failures >= degradedFailureThreshold {
				m.setDegraded(agent, true)
			}
			log.Printf("[agent %s] pull failed (after %d): %v; backoff %s", agent.ID, after, err, backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, pullBackoffCap)
			// Reconnect preflight (D6): distinguish partition (same boot_id —
			// seamless resume below) from daemon death (new boot_id +
			// pi_running:false — drain-then-mark).
			st, perr := m.preflight(agent, ctx)
			if perr == nil && m.bootIDChanged(agent, st.BootID) && !st.PiRunning {
				pendingDeath = true
				backoff = pullBackoffBase // death detected: drain promptly, no outage backoff
			}
			continue
		}
		// Success: the transient flag clears and the failure streak resets.
		failures = 0
		m.setDegraded(agent, false)
		backoff = pullBackoffBase
		for _, rec := range recs {
			// PublishPulled dedupes (spool_id <= watermark → no-op) and
			// advances the watermark transactionally with the append. A
			// store failure must stop the batch: appending later records
			// first would strand the failed one (nextSeq already moved).
			if _, err := agent.hub.PublishPulled(rec.Data, rec.SpoolID); err != nil {
				log.Printf("[agent %s] append pulled record %d failed: %v", agent.ID, rec.SpoolID, err)
				if !sleepCtx(ctx, backoff) {
					return
				}
				break
			}
			m.onPiRecord(agent, rec.Data)
		}
	}
}

// preflight issues GET /status against the daemon with a bounded deadline.
func (m *AgentManager) preflight(agent *Agent, ctx context.Context) (DaemonStatus, error) {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return agent.daemon.Status(pctx)
}

// noteBootID records the daemon's current boot identity if not yet known
// (in-memory only, issue #213). Called at puller start; preflight failures
// are tolerated — boot_id is learned later, comparisons begin then.
func (m *AgentManager) noteBootID(agent *Agent, ctx context.Context) {
	st, err := m.preflight(agent, ctx)
	if err != nil {
		return
	}
	agent.mu.Lock()
	if agent.bootID == "" {
		agent.bootID = st.BootID
	}
	agent.mu.Unlock()
}

// bootIDChanged records st.BootID as the agent's boot identity and reports
// whether it differs from the previously known one (daemon death signature,
// issue #213 D6). An unknown previous boot_id never counts as a change.
func (m *AgentManager) bootIDChanged(agent *Agent, bootID string) bool {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.bootID == "" || bootID == "" || bootID == agent.bootID {
		agent.bootID = bootID
		return false
	}
	agent.bootID = bootID
	return true
}

// setDegraded flips the transient degraded liveness flag (issue #213 D6).
func (m *AgentManager) setDegraded(agent *Agent, v bool) {
	agent.mu.Lock()
	agent.degraded = v
	agent.mu.Unlock()
}

// drainSpool appends every record still on the spool beyond the ack cursor
// (issue #213 D6 drain-then-mark): partial lost-turn records are honest
// history. Reports false when the daemon or the store cannot serve the
// drain — the caller retries instead of marking against a partial drain.
func (m *AgentManager) drainSpool(agent *Agent, ctx context.Context) bool {
	for {
		recs, err := agent.daemon.Records(ctx, agent.hub.Origin(), 0)
		if err != nil {
			log.Printf("[agent %s] death drain failed: %v", agent.ID, err)
			return false
		}
		if len(recs) == 0 {
			return true
		}
		for _, rec := range recs {
			if _, err := agent.hub.PublishPulled(rec.Data, rec.SpoolID); err != nil {
				log.Printf("[agent %s] death drain append %d failed: %v", agent.ID, rec.SpoolID, err)
				return false
			}
			m.onPiRecord(agent, rec.Data)
		}
	}
}

// recordDaemonBoot appends the durable daemon.boot lifecycle event
// (issue #213 D6): {boot_id, pi_running} of the freshly booted daemon. The
// session projection and the agent-status replay fold both ignore it —
// liveness failure is carried by the agent.failed event that follows.
func (m *AgentManager) recordDaemonBoot(agent *Agent, st DaemonStatus) {
	agent.hub.PublishLifecycle(daemonBootEventType, map[string]interface{}{
		"agent_id":   agent.ID,
		"boot_id":    st.BootID,
		"pi_running": st.PiRunning,
	})
	log.Printf("[agent %s] daemon boot recorded (boot_id %s, pi_running %t)", agent.ID, st.BootID, st.PiRunning)
}

// sleepCtx waits d or until ctx is done; reports whether ctx is still live.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
