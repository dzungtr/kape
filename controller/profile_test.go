package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestCreatePartialOverridesCoexist verifies the #177 acceptance criterion:
// two agents with different image/resources/provider/model coexist, and
// omitted fields use config defaults.
func TestCreatePartialOverridesCoexist(t *testing.T) {
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig())

	_, err := mgr.Create(context.Background(), &CreateProfile{
		Image:     "agent-a-image:v1",
		Resources: &Resources{CPU: "2", Memory: "4Gi"},
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	_, err = mgr.Create(context.Background(), &CreateProfile{
		Provider: "custom-provider",
		Model:    "z-ai/glm-5.2",
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}

	reqs := gw.CreatedRequests()
	if len(reqs) != 2 {
		t.Fatalf("created %d sandboxes, want 2", len(reqs))
	}
	a, b := reqs[0], reqs[1]
	if a.Image != "agent-a-image:v1" || a.Provider != "openrouter-spike" || a.Resources != (Resources{CPU: "2", Memory: "4Gi"}) {
		t.Fatalf("agent A spec = %+v, want overridden image/resources, default provider", a)
	}
	if b.Image != "pi-image" || b.Provider != "custom-provider" || b.Resources != (Resources{CPU: "1", Memory: "2Gi"}) {
		t.Fatalf("agent B spec = %+v, want default image/resources, overridden provider", b)
	}
	models := gw.PiModels()
	if models[0] != "" || models[1] != "z-ai/glm-5.2" {
		t.Fatalf("pi models = %v, want [\"\" \"z-ai/glm-5.2\"]", models)
	}
}

// TestCreateWithNameUsesSandboxName verifies the optional name override.
func TestCreateWithNameUsesSandboxName(t *testing.T) {
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig())

	agent, err := mgr.Create(context.Background(), &CreateProfile{Name: "my-agent"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if agent.Sandbox != "my-agent" {
		t.Fatalf("sandbox = %q, want name override", agent.Sandbox)
	}
	if gw.CreatedRequests()[0].Name != "my-agent" {
		t.Fatalf("gateway saw name %q", gw.CreatedRequests()[0].Name)
	}
}

// TestInvalidProfileLeaksNoSandbox is the zero-leak acceptance criterion:
// a rejected create must never reach the gateway.
func TestInvalidProfileLeaksNoSandbox(t *testing.T) {
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig())

	_, err := mgr.Create(context.Background(), &CreateProfile{Resources: &Resources{CPU: "banana", Memory: "2Gi"}})
	if err == nil {
		t.Fatal("invalid profile accepted")
	}
	if reqs := gw.CreatedRequests(); len(reqs) != 0 {
		t.Fatalf("leaked %d sandboxes on rejected create", len(reqs))
	}
	if del := gw.DeletedSandboxes(); len(del) != 0 {
		t.Fatalf("unexpected deletes: %v", del)
	}
}

// TestPiWrapperCommandPatchesBaseUrlAndModel checks the generated wrapper
// script patches both the models.json baseUrl and the model selection.
func TestPiWrapperCommandPatchesBaseUrlAndModel(t *testing.T) {
	cmd := piWrapperCommand("http://gw.internal/v1", "z-ai/glm-5.2")
	script := cmd[2]
	if !strings.Contains(script, `baseUrl"] = "http://gw.internal/v1"`) {
		t.Fatalf("script does not patch baseUrl to configured URL:\n%s", script)
	}
	// Model id is passed as an argv to the python heredoc, never shell-
	// interpolated.
	if !strings.Contains(script, `"z-ai/glm-5.2" <<'PYEOF'`) && !strings.Contains(script, `python3 - "$MODELS" %q`) && !strings.Contains(script, `"z-ai/glm-5.2"`) {
		t.Fatalf("script does not pass model id:\n%s", script)
	}
	if !strings.Contains(script, `if not any(e.get("id") == model for e in models):`) {
		t.Fatalf("script does not ensure model entry:\n%s", script)
	}
	// No model override: baseUrl still patched, model arg is empty so pi
	// keeps the image default selection.
	cmd = piWrapperCommand("http://gw.internal/v1", "")
	script = cmd[2]
	if !strings.Contains(script, `"" <<'PYEOF'`) {
		t.Fatalf("empty model must pass an empty argv, not patch model selection:\n%s", script)
	}
}

// TestHTTPCreateValidationErrorsAre400WithField covers the handler contract:
// invalid values → 400 naming the field, zero sandboxes leaked.
func TestHTTPCreateValidationErrorsAre400WithField(t *testing.T) {
	h, _, gw, _ := newTestHandler(t)

	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"bad image", `{"image":"NOT VALID"}`, "image"},
		{"bad resources", `{"resources":{"cpu":"x","memory":"2Gi"}}`, "resources"},
		{"bad name", `{"name":"Under_Score"}`, "name"},
		{"bad model", `{"model":"model id with space"}`, "model"},
		{"unknown field", `{"nope":1}`, "body"},
		{"bad json", `{`, "body"},
	}
	for _, tc := range cases {
		code, resp := doJSON(t, h, "POST", "/agents", []byte(tc.body))
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", tc.name, code)
		}
		if resp["field"] != tc.field {
			t.Fatalf("%s: field = %v, want %q", tc.name, resp["field"], tc.field)
		}
		if resp["error"] == "" {
			t.Fatalf("%s: missing error text", tc.name)
		}
	}
	if reqs := gw.CreatedRequests(); len(reqs) != 0 {
		t.Fatalf("leaked %d sandboxes across rejected creates", len(reqs))
	}
}

// TestHTTPCreatePartialOverride verifies end-to-end JSON create with overrides.
func TestHTTPCreatePartialOverride(t *testing.T) {
	h, mgr, gw, _ := newTestHandler(t)

	code, resp := doJSON(t, h, "POST", "/agents", []byte(`{"name":"overridden","resources":{"cpu":"2","memory":"4Gi"},"model":"m1"}`))
	if code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", code)
	}
	if mgr.Get(resp["id"].(string)) == nil {
		t.Fatal("agent not registered")
	}
	req := gw.CreatedRequests()[0]
	if req.Name != "overridden" || req.Resources != (Resources{CPU: "2", Memory: "4Gi"}) {
		t.Fatalf("sandbox spec = %+v", req)
	}
	if gw.PiModels()[0] != "m1" {
		t.Fatalf("pi model = %q, want m1", gw.PiModels()[0])
	}
	// Response body shape sanity.
	raw, _ := json.Marshal(resp)
	if !strings.Contains(string(raw), `"status":"ready"`) {
		t.Fatalf("create response missing status: %s", raw)
	}
}
