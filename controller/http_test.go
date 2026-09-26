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
	mgr := NewAgentManager(gw, testConfig())
	return NewRouter(mgr), mgr, gw, stream
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

func TestHTTPAbortWhenIdleIs409Not500(t *testing.T) {
	h, _, _, _ := newTestHandler(t)

	_, create := doJSON(t, h, "POST", "/agents", nil)
	id := create["id"].(string)

	code, _ := doJSON(t, h, "POST", "/agents/"+id+"/abort", nil)
	if code != http.StatusConflict {
		t.Fatalf("abort on idle agent status = %d, want 409", code)
	}
}
