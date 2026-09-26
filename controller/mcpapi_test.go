package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPClientSession connects a real MCP client session to an MCP server
// built over the fake-gateway AgentManager (in-memory transport — same
// protocol handshake and tool dispatch as Streamable HTTP, no loopback
// sockets, which the sandbox forbids).
func newMCPClientSession(t *testing.T) (*mcp.ClientSession, *AgentManager, *FakeGateway, *FakeStream) {
	t.Helper()
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig())

	server := NewMCPServer(mgr)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-host", Version: "v1"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, mgr, gw, stream
}

// mustCreateAgent drives a create through the manager (the MCP create tool
// is covered end-to-end in TestMCPCreateAgentTool) so other tests have an
// agent to point tools at.
func mustCreateAgent(t *testing.T, mgr *AgentManager) *Agent {
	t.Helper()
	agent, err := mgr.Create(context.Background(), &CreateProfile{})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

// callTool invokes an MCP tool and unmarshals its structured output.
func callTool[In any, Out any](t *testing.T, cs *mcp.ClientSession, name string, input In) Out {
	t.Helper()
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s input: %v", name, err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("call %s returned tool error: %s", name, res.Content[0].(*mcp.TextContent).Text)
	}
	var out Out
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s output %s: %v", name, b, err)
	}
	return out
}

// toolContract maps each REST verb to the shared Go struct its MCP tool
// input mirrors — the params half of the contract-equality gate. Fields with
// json:"...,omitempty" are optional; all others are required.
var toolContract = map[string]any{
	"create_agent":  CreateProfile{},
	"get_agent":     agentIDInput{},
	"prompt_agent":  promptInput{},
	"abort":         agentIDInput{},
	"stream_events": streamEventsInput{},
	"read_events":   readEventsInput{},
	"delete_agent":  agentIDInput{},
	"list_agents":   struct{}{},
}

// structParams extracts (properties, required) from a shared input struct's
// json tags, mirroring the SDK's schema generation rules.
func structParams(v any) (props, required map[string]bool) {
	props, required = map[string]bool{}, map[string]bool{}
	ty := reflect.TypeOf(v)
	for ty.Kind() == reflect.Ptr {
		ty = ty.Elem()
	}
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		props[name] = true
		if !strings.Contains(f.Tag.Get("json"), ",omitempty") {
			required[name] = true
		}
	}
	return props, required
}

// TestMCPToolListMatchesRESTContract is the mandatory contract-equality
// assertion: the MCP tool list equals the REST verb set exactly, and each
// tool's input schema matches the params of its shared REST twin struct.
func TestMCPToolListMatchesRESTContract(t *testing.T) {
	cs, _, _, _ := newMCPClientSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	want := RestVerbs()
	if len(got) != len(want) {
		t.Fatalf("MCP tool list %v does not match REST verbs %v", got, want)
	}
	for _, verb := range want {
		if !got[verb] {
			t.Fatalf("MCP tool list missing REST verb %q (have %v)", verb, got)
		}
		delete(got, verb)
	}

	// Params equality: each tool's input schema must match the shared struct.
	for _, tool := range res.Tools {
		wantProps, wantReq := structParams(toolContract[tool.Name])
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal %s schema: %v", tool.Name, err)
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal %s schema: %v", tool.Name, err)
		}
		gotProps := map[string]bool{}
		for p := range schema.Properties {
			gotProps[p] = true
		}
		if len(gotProps) != len(wantProps) {
			t.Fatalf("%s params %v do not match shared struct params %v", tool.Name, gotProps, wantProps)
		}
		for p := range wantProps {
			if !gotProps[p] {
				t.Fatalf("%s missing param %q from shared struct", tool.Name, p)
			}
		}
		gotReq := map[string]bool{}
		for _, r := range schema.Required {
			gotReq[r] = true
		}
		if len(gotReq) != len(wantReq) {
			t.Fatalf("%s required params %v do not match shared struct required %v", tool.Name, schema.Required, wantReq)
		}
		for r := range wantReq {
			if !gotReq[r] {
				t.Fatalf("%s required params missing %q from shared struct", tool.Name, r)
			}
		}
	}
}

// TestMCPCreateAndGetAgentTool round-trips create_agent and get_agent and
// checks the view shape matches the REST twins.
func TestMCPCreateAndGetAgentTool(t *testing.T) {
	cs, mgr, gw, _ := newMCPClientSession(t)

	view := callTool[CreateProfile, AgentView](t, cs, "create_agent", CreateProfile{Model: "m1"})
	if view.ID == "" || view.Status != StatusReady {
		t.Fatalf("create_agent view = %+v, want id + status ready", view)
	}
	if gw.PiModels()[0] != "m1" {
		t.Fatalf("create_agent did not pass model to StartPi: %v", gw.PiModels())
	}

	got := callTool[agentIDInput, AgentView](t, cs, "get_agent", agentIDInput{AgentID: view.ID})
	if got.ID != view.ID || got.Sandbox != view.Sandbox || got.Status != StatusReady {
		t.Fatalf("get_agent = %+v, want same agent as create", got)
	}
	if mgr.Get(view.ID) == nil {
		t.Fatalf("agent %s not registered", view.ID)
	}

	// Unknown agent errors as a tool error, mirroring REST 404.
	if _, _, err := callToolErr(t, cs, "get_agent", agentIDInput{AgentID: "agent-nope"}); err == nil {
		t.Fatal("get_agent unknown id should error")
	}
}

// TestMCPPromptAbortDeleteTools exercises prompt_agent, abort and
// delete_agent, including the 409-equivalent in-flight error.
func TestMCPPromptAbortDeleteTools(t *testing.T) {
	cs, mgr, _, _ := newMCPClientSession(t)
	agent := mustCreateAgent(t, mgr)

	out := callTool[promptInput, map[string]string](t, cs, "prompt_agent", promptInput{AgentID: agent.ID, Message: "turn one"})
	if out["status"] != "accepted" {
		t.Fatalf("prompt_agent = %v, want accepted", out)
	}
	if _, _, err := callToolErr(t, cs, "prompt_agent", promptInput{AgentID: agent.ID, Message: "turn two"}); err == nil {
		t.Fatal("second prompt during turn should error (409-equivalent)")
	}
	if st := callTool[agentIDInput, map[string]string](t, cs, "abort", agentIDInput{AgentID: agent.ID}); st["status"] != "accepted" {
		t.Fatalf("abort = %v, want accepted", st)
	}

	if st := callTool[agentIDInput, map[string]string](t, cs, "delete_agent", agentIDInput{AgentID: agent.ID}); st["status"] != "terminated" {
		t.Fatalf("delete_agent = %v, want terminated", st)
	}
	if _, _, err := callToolErr(t, cs, "delete_agent", agentIDInput{AgentID: agent.ID}); err == nil {
		t.Fatal("delete unknown agent should error")
	}
}

// TestMCPReadEventsToolDefaultsToNonDelta is the #178 core semantic: the MCP
// read_events tool excludes text_delta by default and includes it only on
// include_deltas=true, with cursor+limit+types semantics preserved.
func TestMCPReadEventsToolDefaultsToNonDelta(t *testing.T) {
	cs, mgr, _, _ := newMCPClientSession(t)
	agent := mustCreateAgent(t, mgr)
	agent.hub.Publish(json.RawMessage(`{"type":"text_delta","text":"a"}`))
	agent.hub.Publish(json.RawMessage(`{"type":"text_delta","text":"b"}`))
	agent.hub.Publish(json.RawMessage(`{"type":"message_update","role":"assistant"}`))

	// Default: deltas excluded.
	res := callTool[readEventsInput, readEventsResponse](t, cs, "read_events", readEventsInput{AgentID: agent.ID})
	if res.AgentID != agent.ID {
		t.Fatalf("read_events agent_id = %q", res.AgentID)
	}
	for _, ev := range res.Events {
		if ev.Type == textDeltaEventType {
			t.Fatalf("default read_events returned a text_delta: %+v", ev)
		}
	}
	hasUpdate := false
	for _, ev := range res.Events {
		if ev.Type == "message_update" {
			hasUpdate = true
		}
	}
	if !hasUpdate {
		t.Fatalf("default read_events events = %v, want the message_update present", res.Events)
	}

	// Opt-in: deltas included.
	res = callTool[readEventsInput, readEventsResponse](t, cs, "read_events", readEventsInput{AgentID: agent.ID, IncludeDeltas: true})
	deltas := 0
	for _, ev := range res.Events {
		if ev.Type == textDeltaEventType {
			deltas++
		}
	}
	if deltas != 2 {
		t.Fatalf("include_deltas read_events deltas = %d, want 2 (events %v)", deltas, res.Events)
	}

	// Cursor+limit: after skips earlier seqs; limit bounds the batch.
	all := callTool[readEventsInput, readEventsResponse](t, cs, "read_events", readEventsInput{AgentID: agent.ID, IncludeDeltas: true}).Events
	res = callTool[readEventsInput, readEventsResponse](t, cs, "read_events", readEventsInput{AgentID: agent.ID, After: all[0].Seq, Limit: 1, IncludeDeltas: true})
	if len(res.Events) != 1 || res.Events[0].Seq != all[1].Seq {
		t.Fatalf("after+limit read_events = %v, want seq %d", res.Events, all[1].Seq)
	}

	// types filter composes with the delta exclusion.
	res = callTool[readEventsInput, readEventsResponse](t, cs, "read_events", readEventsInput{AgentID: agent.ID, Types: []string{"text_delta"}})
	if len(res.Events) != 0 {
		t.Fatalf("types=text_delta without include_deltas must be empty, got %v", res.Events)
	}

	// Unknown agent errors (REST 404 equivalent).
	if _, _, err := callToolErr(t, cs, "read_events", readEventsInput{AgentID: "agent-nope"}); err == nil {
		t.Fatal("read_events unknown agent should error")
	}
}

// TestMCPReadEventsExclusionAppliesBeforeLimit pins the filterEventsExcluding
// behaviour the MCP non-delta default relies on: the batch fills with
// matching events, never short-changed by excluded ones.
func TestMCPReadEventsExclusionAppliesBeforeLimit(t *testing.T) {
	h := NewEventHub()
	h.Publish(json.RawMessage(`{"type":"text_delta","text":"a"}`))
	h.Publish(json.RawMessage(`{"type":"text_delta","text":"b"}`))
	h.Publish(json.RawMessage(`{"type":"message_update","role":"assistant"}`))

	res := h.ReadFiltered(0, 1, nil, []string{textDeltaEventType})
	if len(res.Events) != 1 || res.Events[0].Type != "message_update" {
		t.Fatalf("ReadFiltered = %+v, want the message_update only", res.Events)
	}
}

// TestMCPStreamEventsToolReturnsReplay checks the MCP stream_events tool:
// the buffered replay from the cursor in one batch, truncated flag intact.
func TestMCPStreamEventsToolReturnsReplay(t *testing.T) {
	cs, mgr, _, _ := newMCPClientSession(t)
	agent := mustCreateAgent(t, mgr)
	first := agent.hub.Publish(json.RawMessage(`{"type":"text_delta","text":"a"}`))
	agent.hub.Publish(json.RawMessage(`{"type":"agent_settled"}`))

	res := callTool[streamEventsInput, readEventsResponse](t, cs, "stream_events", streamEventsInput{AgentID: agent.ID, After: first})
	if len(res.Events) != 1 || res.Events[0].Type != "agent_settled" {
		t.Fatalf("stream_events after cursor = %v, want only agent_settled", res.Events)
	}
	if _, _, err := callToolErr(t, cs, "stream_events", streamEventsInput{AgentID: "agent-nope"}); err == nil {
		t.Fatal("stream_events unknown agent should error")
	}
}

// TestMCPCreateAgentValidationSurfacesField checks that create-time profile
// validation failures surface as tool errors naming the field (the #177
// handoff consumed by the MCP create tool).
func TestMCPCreateAgentValidationSurfacesField(t *testing.T) {
	cs, _, _, _ := newMCPClientSession(t)
	if _, text, _ := callToolErr(t, cs, "create_agent", CreateProfile{Name: "Bad_Name"}); !strings.Contains(text, "name") {
		t.Fatalf("validation error %q should name the offending field", text)
	}
}

// callToolErr invokes a tool expecting an error result; returns the error text.
func callToolErr[In any](t *testing.T, cs *mcp.ClientSession, name string, input In) (*mcp.CallToolResult, string, error) {
	t.Helper()
	args, _ := json.Marshal(input)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		return res, "", err
	}
	if !res.IsError {
		return res, "", nil
	}
	return res, res.Content[0].(*mcp.TextContent).Text, errToolError
}

// errToolError marks an MCP tool-level error result (as opposed to a
// transport/protocol failure).
var errToolError = errors.New("tool error")

// TestMCPMountedOnRouter checks the MCP endpoint is served on the same mux
// as REST (same port/process): a Streamable HTTP POST to /mcp is handled by
// the MCP transport, not 404'd. Full client handshake needs a loopback
// socket (sandbox-forbidden), so the in-memory-transport session tests above
// exercise the real protocol; this only pins the route wiring.
func TestMCPMountedOnRouter(t *testing.T) {
	h, _, _, _ := newTestHandler(t)
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("/mcp not mounted: %d %s", rec.Code, rec.Body.String())
	}
}

// TestMCPListAgentsTool covers the list_agents call path: an agent created
// via the MCP create tool appears in the list with a live view.
func TestMCPListAgentsTool(t *testing.T) {
	cs, _, _, _ := newMCPClientSession(t)

	view := callTool[CreateProfile, AgentView](t, cs, "create_agent", CreateProfile{Model: "m1"})
	list := callTool[struct{}, []AgentView](t, cs, "list_agents", struct{}{})
	if len(list) != 1 {
		t.Fatalf("list_agents = %+v, want the created agent", list)
	}
	if list[0].ID != view.ID || list[0].Sandbox != view.Sandbox || list[0].Status != StatusReady {
		t.Fatalf("list_agents[0] = %+v, want same agent as create (%+v)", list[0], view)
	}
	if list[0].Source != SourceLive {
		t.Fatalf("list_agents[0].Source = %q, want %q", list[0].Source, SourceLive)
	}
}
