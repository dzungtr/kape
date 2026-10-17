package main

import "testing"

// PiSessionID derives the session-pinned pi --session-id (spec #245 E3,
// issue #249): deterministic from the durable session's agent id, so a
// replaced sandbox on the same PVC resumes the same pi session tree. Agent
// ids stay per-instance naming; the pi id is the session-scoped resume key.
func TestPiSessionIDDeterministic(t *testing.T) {
	tests := []struct {
		name    string
		agentID string
		want    string
	}{
		{name: "root agent id", agentID: "agent-0123456789abcdef", want: "kape-0123456789abcdef"},
		{name: "idempotent", agentID: "agent-0123456789abcdef", want: "kape-0123456789abcdef"},
		{name: "non-agent prefix kept whole", agentID: "weird-id", want: "kape-weird-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PiSessionID(tt.agentID); got != tt.want {
				t.Fatalf("PiSessionID(%q) = %q, want %q", tt.agentID, got, tt.want)
			}
		})
	}
}

// Create threads the session-pinned pi session id into the sandbox create
// (issue #249 AC1): the gateway records it so the daemon env carries it.
func TestCreateThreadsPiSessionID(t *testing.T) {
	mgr, gw, _, agent := newTestAgent(t)
	reqs := gw.CreatedRequests()
	if len(reqs) != 1 {
		t.Fatalf("created %d requests, want 1", len(reqs))
	}
	want := PiSessionID(agent.ID)
	if reqs[0].PiSessionID != want {
		t.Fatalf("CreateSandbox pi session id = %q, want %q", reqs[0].PiSessionID, want)
	}
	// The derived id must be stable across a fresh derivation (revive path).
	if PiSessionID(agent.ID) != want {
		t.Fatalf("derivation not stable: %q vs %q", PiSessionID(agent.ID), want)
	}
	if mgr.Get(agent.ID) == nil {
		t.Fatal("agent not registered")
	}
}

// Sandbox env threading (issue #249 AC1): the pinned pi session id rides the
// sandbox template environment next to the daemon token/agent id.
func TestSandboxEnvPiSessionID(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		agentID     string
		piSessionID string
		want        map[string]string
	}{
		{
			name:        "token plus pinned id",
			token:       "tok",
			agentID:     "agent-1",
			piSessionID: "kape-1",
			want:        map[string]string{"KAPE_DAEMON_TOKEN": "tok", "KAPE_AGENT_ID": "agent-1", "KAPE_PI_SESSION_ID": "kape-1"},
		},
		{
			name:        "pinned id without token (exec break-glass)",
			agentID:     "agent-1",
			piSessionID: "kape-1",
			want:        map[string]string{"KAPE_PI_SESSION_ID": "kape-1"},
		},
		{
			name:        "no pinned id keeps env byte-identical",
			token:       "tok",
			agentID:     "agent-1",
			piSessionID: "",
			want:        map[string]string{"KAPE_DAEMON_TOKEN": "tok", "KAPE_AGENT_ID": "agent-1"},
		},
		{
			name: "nothing set stays empty",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sandboxEnv(tt.token, tt.agentID, tt.piSessionID)
			if len(got) != len(tt.want) {
				t.Fatalf("env = %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("env = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
