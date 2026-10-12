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

// puller backoff on failures (D6): 1s doubling to a 30s cap. Liveness
// flagging (degraded after consecutive failures) is #213's scope.
const (
	pullBackoffBase = 1 * time.Second
	pullBackoffCap  = 30 * time.Second
)

// runPuller drives one agent's pull loop until ctx is cancelled. after starts
// at the hub's recovered origin watermark so restarts never re-append.
func (m *AgentManager) runPuller(agent *Agent, ctx context.Context) {
	backoff := pullBackoffBase
	for {
		after := agent.hub.Origin()
		recs, err := agent.daemon.Records(ctx, after, daemonPullWait)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[agent %s] pull failed (after %d): %v; backoff %s", agent.ID, after, err, backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, pullBackoffCap)
			continue
		}
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
