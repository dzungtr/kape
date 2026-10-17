package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The session-pinned resume argv (spec #245 E3, issue #249): the daemon runs
// pi with --session-dir on the sandbox PVC and a --session-id pinned to the
// session, so a revived sandbox resumes the same pi session tree.
func TestPiArgvSessionPinning(t *testing.T) {
	tests := []struct {
		name       string
		command    []string
		sessionID  string
		sessionDir string
		want       []string
	}{
		{
			name:    "no session id leaves argv unchanged",
			command: []string{"pi", "--mode", "rpc"},
			want:    []string{"pi", "--mode", "rpc"},
		},
		{
			name:       "session id pins default PVC session dir",
			command:    []string{"pi", "--mode", "rpc"},
			sessionID:  "kape-deadbeef",
			sessionDir: "/sandbox/kape/sessions",
			want:       []string{"pi", "--mode", "rpc", "--session-dir", "/sandbox/kape/sessions", "--session-id", "kape-deadbeef"},
		},
		{
			name:       "custom session dir override",
			command:    []string{"pi", "--mode", "rpc"},
			sessionID:  "kape-deadbeef",
			sessionDir: "/tmp/sess",
			want:       []string{"pi", "--mode", "rpc", "--session-dir", "/tmp/sess", "--session-id", "kape-deadbeef"},
		},
		{
			name:       "session dir without session id is ignored",
			command:    []string{"pi", "--mode", "rpc"},
			sessionDir: "/tmp/sess",
			want:       []string{"pi", "--mode", "rpc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := piArgv(tt.command, tt.sessionID, tt.sessionDir)
			if len(got) != len(tt.want) {
				t.Fatalf("argv = %q, want %q", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("argv = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

// External behavior: pi receives the session-pinning flags on its command
// line; the built-in fake pi (flags ignored) stays argv-compatible.
func TestPiStartPassesSessionFlags(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", `printf '%s\n' "$@"`, "-"}, spool, "kape-cafe", "/tmp/sess")
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for pi.Running() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	recs := spool.Read(0, 0)
	if len(recs) < 1 {
		t.Fatal("pi produced no stdout")
	}
	var args []string
	for _, rec := range recs {
		var line string
		if err := json.Unmarshal(rec.Data, &line); err != nil {
			break // pi_exit notice: end of stdout
		}
		if line = strings.TrimSpace(line); line != "" {
			args = append(args, line)
		}
	}
	want := []string{"--session-dir", "/tmp/sess", "--session-id", "kape-cafe"}
	if len(args) != len(want) {
		t.Fatalf("pi argv tail = %q, want %q", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("pi argv tail = %q, want %q", args, want)
		}
	}
}

// The controller threads the pinned id via env; the daemon picks it up with
// the PVC default dir (spec #245 E3 plumbing).
func TestSessionEnvConfig(t *testing.T) {
	t.Setenv("KAPE_PI_SESSION_ID", "kape-abc123")
	t.Setenv("KAPE_PI_SESSION_DIR", "")
	cfg := parseConfig([]string{})
	if cfg.PiSessionID != "kape-abc123" {
		t.Fatalf("PiSessionID = %q, want kape-abc123", cfg.PiSessionID)
	}
	if cfg.PiSessionDir != "/sandbox/kape/sessions" {
		t.Fatalf("PiSessionDir = %q, want /sandbox/kape/sessions default", cfg.PiSessionDir)
	}
	t.Setenv("KAPE_PI_SESSION_DIR", "/tmp/other")
	cfg = parseConfig([]string{})
	if cfg.PiSessionDir != "/tmp/other" {
		t.Fatalf("PiSessionDir = %q, want /tmp/other", cfg.PiSessionDir)
	}
	t.Setenv("KAPE_PI_SESSION_ID", "")
	t.Setenv("KAPE_PI_SESSION_DIR", "")
	cfg = parseConfig([]string{})
	if cfg.PiSessionID != "" || cfg.PiSessionDir != "/sandbox/kape/sessions" {
		t.Fatalf("unset env: id=%q dir=%q", cfg.PiSessionID, cfg.PiSessionDir)
	}
}
