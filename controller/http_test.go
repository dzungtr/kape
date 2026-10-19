package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer wires a NewRouter over an AgentManager backed by the fake
// gateway and returns the server plus the fakes for driving the FSM.
func newTestHandler(t *testing.T) (http.Handler, *AgentManager, *FakeGateway, *FakeStream) {
	t.Helper()
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig(), testStore(t))
	return NewRouter(mgr, ""), mgr, gw, stream
}

// doJSON issues a request directly against the router handler via
// httptest.NewRecorder (no loopback sockets needed in the sandbox).
func doJSON(t *testing.T, h http.Handler, method, target string, body []byte) (int, map[string]interface{}) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var m map[string]interface{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, m
}

func TestHTTPSecondPromptDuringTurnIs409(t *testing.T) {
	h, _, _, _ := newTestHandler(t)

	code, create := doJSON(t, h, "POST", "/agents", nil)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", code)
	}
	// Create response must include status (consistent with GET detail).
	if create["status"] != string(StatusReady) {
		t.Fatalf("create response status = %v, want %q", create["status"], StatusReady)
	}
	id := create["id"].(string)

	code, _ = doJSON(t, h, "POST", "/agents/"+id+"/prompt", []byte(`{"message":"turn one"}`))
	if code != http.StatusAccepted {
		t.Fatalf("first prompt status = %d, want 202", code)
	}
	code, body := doJSON(t, h, "POST", "/agents/"+id+"/prompt", []byte(`{"message":"turn two"}`))
	if code != http.StatusConflict {
		t.Fatalf("second prompt status = %d, want 409", code)
	}
	if body["error"] == nil {
		t.Fatalf("409 body %v missing error field", body)
	}
}

func TestHTTPPromptToUnknownAgentIs404(t *testing.T) {
	h, _, _, _ := newTestHandler(t)

	code, _ := doJSON(t, h, "POST", "/agents/agent-nope/prompt", []byte(`{"message":"hi"}`))
	if code != http.StatusNotFound {
		t.Fatalf("prompt unknown agent status = %d, want 404", code)
	}
	code, _ = doJSON(t, h, "GET", "/agents/agent-nope", nil)
	if code != http.StatusNotFound {
		t.Fatalf("get unknown agent status = %d, want 404", code)
	}
}

// TestHTTPSteerRouteAndPolicy covers the #215 front-door steer endpoint:
// 202 mid-turn, 409 on an idle agent, 404 unknown, 400 empty message.
func TestHTTPSteerRouteAndPolicy(t *testing.T) {
	h, mgr, _, stream := newTestHandler(t)

	_, create := doJSON(t, h, "POST", "/agents", nil)
	id := create["id"].(string)

	// Idle: 409-equivalent, and no steer reaches pi.
	code, _ := doJSON(t, h, "POST", "/agents/"+id+"/steer", []byte(`{"message":"focus"}`))
	if code != http.StatusConflict {
		t.Fatalf("steer on idle agent status = %d, want 409", code)
	}

	// Mid-turn: 202, and the pi steer command is on the exec stdin.
	code, _ = doJSON(t, h, "POST", "/agents/"+id+"/prompt", []byte(`{"message":"turn one"}`))
	if code != http.StatusAccepted {
		t.Fatalf("prompt status = %d, want 202", code)
	}
	code, _ = doJSON(t, h, "POST", "/agents/"+id+"/steer", []byte(`{"message":"switch to the test file"}`))
	if code != http.StatusAccepted {
		t.Fatalf("mid-turn steer status = %d, want 202", code)
	}
	agent := mgr.Get(id)
	if agent == nil || agent.Status() != StatusWorking {
		t.Fatalf("status after steer = %q, want working", agent.Status())
	}
	// The pi RPC steer command rides the exec stdin (prompt's shape, type
	// steer) so real pi consumes it (README rpc-commands shapes). The stdin
	// pump is async — wait for the record to land on the stream.
	var steerSent []byte
	waitFor(t, func() bool {
		for _, s := range stream.SentPayloads() {
			if bytes.Contains(s, []byte(`"type":"steer"`)) {
				steerSent = s
				return true
			}
		}
		return false
	}, "steer record on exec stdin")
	var cmd struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if steerSent == nil || json.Unmarshal(steerSent, &cmd) != nil || cmd.Type != "steer" || cmd.ID == "" || cmd.Message != "switch to the test file" {
		t.Fatalf("pi steer command = %q, want {id,type:steer,message}", steerSent)
	}

	// Validation and routing edges.
	code, _ = doJSON(t, h, "POST", "/agents/"+id+"/steer", []byte(`{"message":""}`))
	if code != http.StatusBadRequest {
		t.Fatalf("empty-message steer status = %d, want 400", code)
	}
	code, _ = doJSON(t, h, "POST", "/agents/agent-nope/steer", []byte(`{"message":"x"}`))
	if code != http.StatusNotFound {
		t.Fatalf("steer unknown agent status = %d, want 404", code)
	}
}

func TestHTTPAbortWhenIdleIs409Not500(t *testing.T) {
	h, _, _, _ := newTestHandler(t)

	_, create := doJSON(t, h, "POST", "/agents", nil)
	id := create["id"].(string)

	code, _ := doJSON(t, h, "POST", "/agents/"+id+"/abort", nil)
	if code != http.StatusConflict {
		t.Fatalf("abort on idle agent status = %d, want 409", code)
	}
}
