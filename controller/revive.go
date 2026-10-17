package main

// Tier-2 revive (issue #251, spec #245 E5/E7): on sandbox/daemon death
// detection (new boot_id — under the #247 canonical-main-process posture the
// daemon's new boot id means the driver re-created the pod on the same
// Sandbox CR and PVC) the session FSM takes the running → paused edge, the
// log-derived budget is spent, and the puller holds until the replacement
// sandbox's daemon answers. daemon.boot + the first successful pull then
// drive paused → running; the turn that was in flight at death is closed as
// an unsettled revive-settle — no auto-reinject (E7): accepted-but-
// undelivered commands are derivable from the log and hosts re-send.
//
// Budget: REVIVE_MAX (KAPE_REVIVE_MAX, default 3) Tier-2 revives per session
// lifetime, REVIVE_COOLDOWN (KAPE_REVIVE_COOLDOWN, default 60s) minimum
// spacing between attempts. The ledger is log-derived — never an in-memory
// counter — so replaying the log lands on the identical budget. The re-arm
// front-door verb (resume_session / POST /agents/{id}/resume) appends
// agent.resumed, which resets the ledger, and re-drives the revive.
//
// Exhaustion: the session stays paused with agent liveness failed, surfaced
// via wait_agent (agent.paused is a session lifecycle wake; agent.failed is
// the liveness wake). Delete from paused terminates (paused → terminated).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// reviveConsumedEventType is the non-FSM budget-ledger event appended when a
// Tier-2 revive attempt begins: {attempt, at}. The session projection and
// the agent-status replay fold ignore it (revive.go owns the ledger fold).
const reviveConsumedEventType = "agent.revive_consumed"

// agentResumedEventType is the re-arm ledger event appended by the
// resume_session front door: it resets the revive budget for the session.
const agentResumedEventType = "agent.resumed"

// reviveWaitDeadline bounds the hold for the replacement sandbox's daemon
// (the pod re-creation window; matches waitReady's provision budget). On
// expiry the session stays paused + failed; a re-arm re-drives the revive.
const reviveWaitDeadline = 10 * time.Minute

// revivePollInterval is the preflight poll period while holding for the
// replacement daemon.
const revivePollInterval = 500 * time.Millisecond

// reviveLedger is the log-derived Tier-2 revive budget ledger.
type reviveLedger struct {
	used        int       // revive attempts consumed since the last re-arm
	lastAttempt time.Time // RFC3339Nano `at` of the latest consumption
	// openAttempt: a consumption has no completion marker (agent.revived)
	// after it — a controller restart mid-revive replays it, and the resumed
	// wait must not spend budget twice.
	openAttempt bool
}

// reviveLedgerFromEvents folds the ledger over a log slice. Only the ledger
// event types touch it; everything else is ignored.
func reviveLedgerFromEvents(evs []Event) reviveLedger {
	var l reviveLedger
	for i := range evs {
		switch evs[i].Type {
		case reviveConsumedEventType:
			l.used++
			l.openAttempt = true
			var f struct {
				At string `json:"at"`
			}
			if json.Unmarshal(evs[i].Data, &f) == nil {
				if t, err := time.Parse(time.RFC3339Nano, f.At); err == nil {
					l.lastAttempt = t
				}
			}
		case lifecycleRevived:
			l.openAttempt = false
		case agentResumedEventType:
			l = reviveLedger{} // re-arm: budget reset (spec #245 E5)
		}
	}
	return l
}

// remaining reports the revive attempts left under the given budget cap.
func (l reviveLedger) remaining(max int) int {
	r := max - l.used
	if r < 0 {
		return 0
	}
	return r
}

// cooldownRemaining reports how much of the cooldown spacing still separates
// now from the next legal revive attempt (zero when the last attempt is
// older than the cooldown, or when no attempt is recorded).
func (l reviveLedger) cooldownRemaining(now time.Time, cooldown time.Duration) time.Duration {
	if l.lastAttempt.IsZero() || cooldown <= 0 {
		return 0
	}
	d := cooldown - now.Sub(l.lastAttempt)
	if d < 0 {
		return 0
	}
	return d
}

// reviveLedgerOf derives the agent's ledger from its durable log snapshot,
// returning the snapshot too (the boot-id cross-check reads the same slice).
func (m *AgentManager) reviveLedgerOf(agent *Agent) (reviveLedger, []Event) {
	evs, err := agent.hub.SubscribeSnapshot()
	if err != nil {
		// Ledger derivation failure: treat as exhausted — durability wins
		// over availability (EventHub policy); the re-arm verb can retry.
		log.Printf("[agent %s] revive ledger read failed: %v", agent.ID, err)
		return reviveLedger{used: 1 << 30}, nil
	}
	return reviveLedgerFromEvents(evs), evs
}

// lastRecordedBootID returns the boot_id of the latest daemon.boot in the
// slice ("" when none) — the revive boot-event cross-check: a replacement
// whose boot id is already on the log is not re-recorded.
func lastRecordedBootID(evs []Event) string {
	last := ""
	for i := range evs {
		if evs[i].Type != daemonBootEventType {
			continue
		}
		var f struct {
			BootID string `json:"boot_id"`
		}
		if json.Unmarshal(evs[i].Data, &f) == nil {
			last = f.BootID
		}
	}
	return last
}

// recordBootOnce records the replacement daemon's boot event only when its
// boot id is not already the latest recorded one — an unchanged boot (the
// attempt's daemon is the one already recorded pre-restart) is not
// re-recorded, keeping one daemon.boot per boot identity.
func (m *AgentManager) recordBootOnce(agent *Agent, st DaemonStatus) {
	if st.BootID == "" {
		return
	}
	evs, err := agent.hub.SubscribeSnapshot()
	if err != nil {
		return
	}
	if st.BootID != lastRecordedBootID(evs) {
		m.recordDaemonBoot(agent, st)
	}
}

// pauseSession takes the session into paused (the running → paused edge,
// spec #245 E5) and flips agent liveness to failed — the instance that died
// is gone; the replacement is a new one. Reports false when the edge is
// illegal (the session is not running — revive is pinned to running → paused;
// the caller keeps the #213 legacy drain-then-mark path for idle sessions).
func (m *AgentManager) pauseSession(agent *Agent, reason string) bool {
	if err := agent.fsm.Transition(SessionPaused, lifecyclePaused, map[string]interface{}{"reason": reason}); err != nil {
		log.Printf("[agent %s] pause rejected: %v", agent.ID, err)
		return false
	}
	m.fail(agent, "sandbox death: "+reason) // liveness failed + monitor disarm; the session FSM stays paused
	return true
}

// pauseAndRevive runs the Tier-2 revive decision after death detection and a
// complete spool drain: pause the session, spend budget (cooldown-paced),
// hold for the replacement daemon, and record its boot identity. Reports
// true when the puller must stop — budget exhausted or the replacement never
// answered — leaving the session paused + failed; only the re-arm verb (or a
// controller restart resuming an open attempt) drives the next try.
func (m *AgentManager) pauseAndRevive(agent *Agent, ctx context.Context) bool {
	if !m.pauseSession(agent, "sandbox death detected (new boot_id)") {
		return true
	}
	// The ledger decision (read → cooldown → consume) is serialized per
	// agent: concurrent revive decisions (a fresh death racing a re-arm or
	// replay revival) must never double-spend or interleave ledger resets.
	agent.reviveMu.Lock()
	led, _ := m.reviveLedgerOf(agent)
	if led.remaining(m.cfg.ReviveMax) <= 0 {
		agent.reviveMu.Unlock()
		log.Printf("[agent %s] revive budget exhausted (%d used of %d); session stays paused + failed", agent.ID, led.used, m.cfg.ReviveMax)
		return true
	}
	if d := led.cooldownRemaining(m.now(), m.cfg.ReviveCooldown); d > 0 {
		log.Printf("[agent %s] revive cooldown: holding %s", agent.ID, d)
		if !sleepCtx(ctx, d) {
			agent.reviveMu.Unlock()
			return true
		}
	}
	m.recordReviveConsumed(agent, led.used+1)
	agent.reviveMu.Unlock()
	st, ok := m.waitForReplacementDaemon(agent, ctx)
	if !ok {
		return true
	}
	m.recordBootOnce(agent, st)
	m.setBootID(agent, st.BootID)
	m.armPendingRevive(agent)
	log.Printf("[agent %s] replacement daemon up (boot_id %s); awaiting first pull to resume", agent.ID, st.BootID)
	return false
}

// reviveFromPaused re-drives a revive for an already-paused session: the
// re-arm verb (fresh or reset budget) and the controller-restart replay of a
// paused session whose attempt is still open. An open attempt (a
// revive_consumed with no agent.revived after it) is resumed without
// spending budget again.
func (m *AgentManager) reviveFromPaused(agent *Agent, ctx context.Context) {
	agent.reviveMu.Lock()
	led, _ := m.reviveLedgerOf(agent)
	if led.remaining(m.cfg.ReviveMax) <= 0 {
		agent.reviveMu.Unlock()
		log.Printf("[agent %s] revive budget exhausted; re-arm required", agent.ID)
		return
	}
	if !led.openAttempt {
		if d := led.cooldownRemaining(m.now(), m.cfg.ReviveCooldown); d > 0 {
			if !sleepCtx(ctx, d) {
				agent.reviveMu.Unlock()
				return
			}
		}
		m.recordReviveConsumed(agent, led.used+1)
	}
	agent.reviveMu.Unlock()
	m.holdAndResumePuller(agent, ctx)
}

// holdAndResumePuller runs the revive hold: wait for the replacement daemon,
// record its boot identity once, arm the pending-revive flag, and restart
// the pull loop — its first successful pull completes paused → running.
func (m *AgentManager) holdAndResumePuller(agent *Agent, ctx context.Context) {
	st, ok := m.waitForReplacementDaemon(agent, ctx)
	if !ok {
		return
	}
	m.recordBootOnce(agent, st)
	m.setBootID(agent, st.BootID)
	m.armPendingRevive(agent)
	go m.runPuller(agent, ctx)
}

// recordReviveConsumed appends the budget-ledger event for one revive
// attempt: {attempt, at}. Non-FSM: the session projection ignores it.
func (m *AgentManager) recordReviveConsumed(agent *Agent, attempt int) {
	agent.hub.PublishLifecycle(reviveConsumedEventType, map[string]interface{}{
		"agent_id": agent.ID,
		"attempt":  attempt,
		"at":       m.now().UTC().Format(time.RFC3339Nano),
	})
}

// waitForReplacementDaemon holds until the replacement sandbox's daemon
// answers a preflight (pod re-creation window). The daemon may answer with
// pi_running:false — pi is spawned lazily on the next command (#248) — so
// reachability, not pi liveness, is the gate. On each failed preflight the
// cached client is dropped so the address re-resolves from the Sandbox CR
// (D7). Reports false on deadline expiry or context end.
func (m *AgentManager) waitForReplacementDaemon(agent *Agent, ctx context.Context) (DaemonStatus, bool) {
	deadline := m.now().Add(reviveWaitDeadline)
	for {
		st, err := m.preflight(agent, ctx)
		if err == nil {
			return st, true
		}
		m.invalidateDaemon(agent) // podIPs churn with the replacement pod (D7)
		if !m.now().Before(deadline) {
			log.Printf("[agent %s] replacement daemon not reachable within %s; session stays paused", agent.ID, reviveWaitDeadline)
			return DaemonStatus{}, false
		}
		if !sleepCtx(ctx, revivePollInterval) {
			return DaemonStatus{}, false
		}
	}
}

// armPendingRevive marks the agent so the puller's next successful pull
// completes the paused → running edge (daemon.boot + first successful pull).
func (m *AgentManager) armPendingRevive(agent *Agent) {
	agent.mu.Lock()
	agent.pendingRevive = true
	agent.mu.Unlock()
}

// completeRevive finishes a revive on the first successful pull of the
// replacement sandbox: paused → running, then the lost in-flight turn is
// closed as an unsettled revive-settle (running → idle) so the session can
// accept a new prompt — no auto-reinject (spec #245 E7): the host re-sends.
// The controller-synthesized settle carries revive_settled:true in the log.
func (m *AgentManager) completeRevive(agent *Agent) {
	agent.mu.Lock()
	pending := agent.pendingRevive
	agent.pendingRevive = false
	agent.mu.Unlock()
	if !pending {
		return
	}
	if agent.fsm.State() != SessionPaused {
		return // revived elsewhere (or re-paused by a fresh death): nothing to complete
	}
	if err := agent.fsm.Transition(SessionRunning, lifecycleRevived, nil); err != nil {
		log.Printf("[agent %s] record revived failed: %v", agent.ID, err)
		return
	}
	if err := agent.fsm.Transition(SessionIdle, lifecycleSettled, map[string]interface{}{"revive_settled": true}); err != nil {
		log.Printf("[agent %s] record revive-settle failed: %v", agent.ID, err)
	}
	agent.mu.Lock()
	agent.status = StatusReady
	agent.mu.Unlock()
	log.Printf("[agent %s] revived: paused → running; lost turn closed unsettled (hosts re-send, E7)", agent.ID)
}

// Resume is the re-arm front-door verb (spec #245 E5): resets the log-derived
// revive budget (agent.resumed) and re-drives the revive for a paused
// session. Only a paused session re-arms — anything else is the 409
// equivalent.
func (m *AgentManager) Resume(ctx context.Context, id string) error {
	agent := m.Get(id)
	if agent == nil {
		return errUnknownAgent(id)
	}
	if agent.fsm.State() != SessionPaused {
		return errNotReady(id, agent.Status())
	}
	agent.reviveMu.Lock()
	led, _ := m.reviveLedgerOf(agent)
	if led.openAttempt {
		// A revive attempt is already open (consumed, not yet completed):
		// re-arming again would reset the ledger mid-attempt — fail-fast.
		agent.reviveMu.Unlock()
		return errNotReady(id, agent.Status())
	}
	if agent.hub.PublishLifecycle(agentResumedEventType, map[string]interface{}{"agent_id": agent.ID}) == nil {
		agent.reviveMu.Unlock()
		return fmt.Errorf("record re-arm for %s failed", id)
	}
	if agent.pullTransport {
		// The ledger reset (agent.resumed above) and the attempt consumption
		// land together, under the revive lock: from the moment Resume
		// returns the attempt is open, so a concurrent second Resume is
		// rejected instead of resetting the ledger mid-attempt.
		m.recordReviveConsumed(agent, led.used+1)
	}
	agent.reviveMu.Unlock()
	if agent.pullTransport {
		ctx, cancel := context.WithCancel(context.Background())
		agent.mu.Lock()
		if agent.cancel != nil {
			agent.cancel()
		}
		agent.cancel = cancel
		agent.mu.Unlock()
		go m.holdAndResumePuller(agent, ctx)
	}
	log.Printf("[agent %s] re-armed: revive budget reset, revive re-driven", id)
	return nil
}
