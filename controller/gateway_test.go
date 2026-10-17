package main

import (
	"testing"
)

// createSandboxRequest builds the CreateSandboxRequest the gateway create
// path sends. Issue #247 (spec #245 E2): Spec.Command carries the daemon as
// the sandbox's canonical main process — no shell wrapping, no profile hook.

func TestCreateSandboxRequest_ThreadsDaemonCommand(t *testing.T) {
	req := createSandboxRequest("sbx-test", "img:1", "prov",
		Resources{CPU: "1", Memory: "2Gi"},
		map[string]string{"managed-by": "kape-controller"},
		map[string]string{"KAPE_DAEMON_TOKEN": "t"},
		[]string{"kape-daemon"})
	if got := req.Spec.Command; len(got) != 1 || got[0] != "kape-daemon" {
		t.Fatalf("Spec.Command = %v, want [kape-daemon] (proto field 12, no shell wrapping)", got)
	}
	if req.Spec.Template.Image != "img:1" {
		t.Fatalf("image = %q", req.Spec.Template.Image)
	}
	if req.Spec.Template.Environment["KAPE_DAEMON_TOKEN"] != "t" {
		t.Fatal("daemon token env missing")
	}
}

func TestDaemonEnv_MainProcessInjection(t *testing.T) {
	gw := (&Gateway{}).WithDaemonCommand([]string{"kape-daemon"}).WithDaemonToken("t")
	env := gw.daemonEnv(map[string]string{LabelAgentID: "agent-1"}, "")
	if env["KAPE_DAEMON_TOKEN"] != "t" || env["KAPE_AGENT_ID"] != "agent-1" {
		t.Fatalf("token/agent env missing: %v", env)
	}
	if env["KAPE_SPOOL_PATH"] != "/sandbox/kape-daemon-spool.jsonl" {
		t.Fatal("PVC spool path env missing (main process has no profile hook)")
	}
}

func TestDaemonEnv_SpoolPathOnlyWhenCommandThreaded(t *testing.T) {
	// Fallback posture: no command → no main-process env injection beyond
	// the D5 token channel (unchanged behavior).
	gw := (&Gateway{}).WithDaemonToken("t")
	env := gw.daemonEnv(map[string]string{}, "")
	if _, ok := env["KAPE_SPOOL_PATH"]; ok {
		t.Fatalf("KAPE_SPOOL_PATH injected without canonical command: %v", env)
	}
	if env["KAPE_DAEMON_TOKEN"] != "t" {
		t.Fatalf("token channel lost in fallback posture: %v", env)
	}
	// No token and no command → nil env, sandbox creation byte-identical.
	if env := (&Gateway{}).daemonEnv(map[string]string{}, ""); env != nil {
		t.Fatalf("bare gateway env = %v, want nil", env)
	}
}

func TestCreateSandboxRequest_EmptyCommandOmitted(t *testing.T) {
	// Fallback posture: empty configured command leaves the request without
	// Spec.Command so the gateway's own default main process applies.
	req := createSandboxRequest("sbx-test", "img:1", "prov", Resources{CPU: "1", Memory: "1Gi"}, nil, nil, nil)
	if len(req.Spec.Command) != 0 {
		t.Fatalf("Spec.Command = %v, want omitted", req.Spec.Command)
	}
}

func TestCreateSandboxRequest_ProtoFieldNumber(t *testing.T) {
	// The vendored proto pins command to field 12 with the E2 wording; the
	// wire contract must not drift silently.
	req := createSandboxRequest("sbx-test", "img", "prov", Resources{CPU: "1", Memory: "1Gi"}, nil, nil, []string{"kape-daemon"})
	md := req.Spec.ProtoReflect().Descriptor().Fields().ByJSONName("command")
	if md == nil {
		t.Fatal("SandboxSpec.command field missing from vendored proto")
	}
	if got := md.Number(); got != 12 {
		t.Fatalf("command field number = %d, want 12", got)
	}
}

func TestDaemonEnv_MergedSessionIDAndSpoolPath(t *testing.T) {
	// Merge of #255 (E3, issue #249) with #257 (E2, issue #247): the pinned
	// pi session id rides the D5 token channel (sandboxEnv), and the PVC
	// spool path is injected only in the canonical main-process posture.
	gw := (&Gateway{}).WithDaemonCommand([]string{"kape-daemon"}).WithDaemonToken("t")
	env := gw.daemonEnv(map[string]string{LabelAgentID: "agent-1"}, "kape-1")
	if env["KAPE_PI_SESSION_ID"] != "kape-1" {
		t.Fatalf("pinned id channel lost in main-process posture: %v", env)
	}
	if env["KAPE_SPOOL_PATH"] != "/sandbox/kape-daemon-spool.jsonl" {
		t.Fatalf("PVC spool path env missing alongside the pinned id: %v", env)
	}
	if env["KAPE_DAEMON_TOKEN"] != "t" || env["KAPE_AGENT_ID"] != "agent-1" {
		t.Fatalf("token/agent env missing: %v", env)
	}
}
