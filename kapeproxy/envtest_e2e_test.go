//go:build envtest

package kapeproxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/infra/ports"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"

	proxy "github.com/kape-io/kape/kapeproxy/internal/proxy"
)

func TestKapeproxyE2E_ToolsListAndCall(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — skipping envtest")
	}

	// 1. Start an in-process mock MCP upstream serving two tools.
	mockMCPAddr := startE2EMockMCPServer(t, []string{"ping", "echo"}, map[string]any{"pong": true})

	// 2. Boot envtest.
	e2eScheme := func() *runtime.Scheme {
		s := runtime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(s))
		utilruntime.Must(appsv1.AddToScheme(s))
		utilruntime.Must(corev1.AddToScheme(s))
		utilruntime.Must(v1alpha1.AddToScheme(s))
		return s
	}()

	env := &envtest.Environment{
		CRDDirectoryPaths: []string{"../operator/crds"},
		Scheme:            e2eScheme,
	}
	restCfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// 3. Build and start the controller manager.
	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 e2eScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	require.NoError(t, err)

	k8sClient := mgr.GetClient()

	platformCfg := domainconfig.KapeConfig{
		ClusterName:          "envtest-e2e",
		HandlerImage:         "kape-runtime",
		HandlerImageVersion:  "dev",
		DefaultMaxIterations: 10,
	}
	cfgLoader := &e2eKapeConfigLoader{cfg: platformCfg}

	handlerRec := reconcilehandler.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(k8sClient),
		k8sadapters.NewSchemaRepository(k8sClient),
		k8sadapters.NewToolRepository(k8sClient),
		k8sadapters.NewSkillRepository(k8sClient),
		k8sadapters.NewConfigMapAdapter(k8sClient),
		k8sadapters.NewKapeproxyConfigAdapter(k8sClient),
		k8sadapters.NewSkillConfigMapAdapter(k8sClient),
		k8sadapters.NewServiceAccountAdapter(k8sClient),
		k8sadapters.NewDeploymentAdapter(k8sClient),
		k8sadapters.NewScaledObjectAdapter(k8sClient),
		tomlrenderer.NewRenderer(),
		cfgLoader,
	)
	require.NoError(t, kapecontroller.SetupHandlerReconciler(mgr, handlerRec, 1))

	toolRec := reconcilehandler.NewToolReconciler(
		k8sadapters.NewToolRepository(k8sClient),
		k8sadapters.NewStatefulSetAdapter(k8sClient),
		cfgLoader,
	)
	require.NoError(t, kapecontroller.SetupToolReconciler(mgr, toolRec, 1))

	schemaRec := reconcilehandler.NewSchemaReconciler(
		k8sadapters.NewSchemaRepository(k8sClient),
		mgr.GetEventRecorderFor("kapeschema-controller"),
	)
	require.NoError(t, kapecontroller.SetupSchemaReconciler(mgr, schemaRec, 1))

	go func() { _ = mgr.Start(ctx) }()

	ns := "default"

	// 4. Apply prerequisite KapeSchema (required by KapeHandlerSpec).
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-schema", Namespace: ns},
		Spec: v1alpha1.KapeSchemaSpec{
			Version: "v1",
			JSONSchema: v1alpha1.JSONSchemaObject{
				Type:     "object",
				Required: []string{"msg"},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, schema))
	schema.Status.Conditions = []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionTrue, Reason: "Valid",
	}}
	require.NoError(t, k8sClient.Status().Update(ctx, schema))

	// 5. Apply the mcp-type KapeTool pointing at the in-process mock.
	mockURL := fmt.Sprintf("http://%s", mockMCPAddr)
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "stub-mcp", Namespace: ns},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream: v1alpha1.MCPUpstreamSpec{
					Transport: "streamable-http",
					URL:       mockURL,
				},
				AllowedTools: []string{"ping", "echo"},
				SkipProbe:    true,
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, tool))

	// 6. Apply a KapeHandler referencing the tool.
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-handler", Namespace: ns},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test"},
			SchemaRef: "e2e-schema",
			Tools:     []v1alpha1.ToolRef{{Ref: "stub-mcp"}},
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, handler))

	// 7. Wait for the reconciler to render the kapeproxy-config ConfigMap.
	cmKey := types.NamespacedName{Name: "kapeproxy-config-e2e-handler", Namespace: ns}
	var configMap corev1.ConfigMap
	require.Eventually(t, func() bool {
		return k8sClient.Get(ctx, cmKey, &configMap) == nil && configMap.Data["config.yaml"] != ""
	}, 15*time.Second, 200*time.Millisecond, "kapeproxy-config ConfigMap must be created by reconciler")

	// 8. Parse the rendered config.yaml and build an in-process kapeproxy Server.
	var proxyCfg proxy.Config
	require.NoError(t, yaml.Unmarshal([]byte(configMap.Data["config.yaml"]), &proxyCfg))

	upstreams := make(map[string]proxy.Upstream)
	for name, upCfg := range proxyCfg.Upstreams {
		upstreams[name] = proxy.NewMCPUpstream(ctx, name, upCfg)
	}

	router := proxy.NewRouter(&proxyCfg, upstreams)
	redactor := proxy.NewRedactor()
	logger := zerolog.New(zerolog.NewTestWriter(t))
	auditLogger := proxy.NewAuditLogger(logger)

	proxyAddr := e2eFreeAddr(t)
	srv := proxy.NewServer(proxyAddr, router, redactor, auditLogger, logger)
	go func() { _ = srv.Start() }()
	t.Cleanup(func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	})

	// Give the server a moment to bind.
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", proxyAddr)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 5*time.Second, 50*time.Millisecond, "kapeproxy server should start")

	proxyURL := "http://" + proxyAddr

	// 9. Assert tools/list returns namespaced tool names.
	listResp := e2eDoJSONRPC(t, proxyURL, "tools/list", nil)
	require.Nil(t, listResp.Error, "tools/list must not error")
	result, ok := listResp.Result.(map[string]any)
	require.True(t, ok)
	toolsRaw, ok := result["tools"].([]any)
	require.True(t, ok)

	toolNames := make([]string, 0, len(toolsRaw))
	for _, item := range toolsRaw {
		m, ok := item.(map[string]any)
		require.True(t, ok)
		toolNames = append(toolNames, m["name"].(string))
	}
	assert.Contains(t, toolNames, "stub-mcp__ping", "tools/list should include stub-mcp__ping")
	assert.Contains(t, toolNames, "stub-mcp__echo", "tools/list should include stub-mcp__echo")

	// 10. Assert tools/call for an allowed tool returns a result.
	callResp := e2eDoJSONRPC(t, proxyURL, "tools/call", map[string]any{
		"name":      "stub-mcp__ping",
		"arguments": map[string]any{},
	})
	assert.Nil(t, callResp.Error, "tools/call stub-mcp__ping must succeed")
	assert.NotNil(t, callResp.Result)

	// 11. Assert tools/call for a disallowed tool returns -32601.
	disallowedResp := e2eDoJSONRPC(t, proxyURL, "tools/call", map[string]any{
		"name":      "stub-mcp__not-allowed",
		"arguments": map[string]any{},
	})
	require.NotNil(t, disallowedResp.Error)
	assert.Equal(t, -32601, disallowedResp.Error.Code, "disallowed tool should return MCP method-not-found")
}

// startE2EMockMCPServer spins up an in-process MCP server using the real SDK.
func startE2EMockMCPServer(t *testing.T, tools []string, staticResult any) string {
	t.Helper()
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "mock-mcp", Version: "0.0.1"}, nil)
	for _, name := range tools {
		n := name
		res := staticResult
		mcpSrv.AddTool(&mcp.Tool{
			Name:        n,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			b, _ := json.Marshal(res)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
			}, nil
		})
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	httpSrv := &http.Server{
		Handler:           mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return mcpSrv }, nil),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = httpSrv.Serve(listener) }()
	t.Cleanup(func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = httpSrv.Shutdown(shutCtx)
	})
	return addr
}

// e2eFreeAddr returns a free host:port on loopback.
func e2eFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

type e2eJSONRPCResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  any             `json:"result"`
	Error   *e2eJSONRPCErr  `json:"error"`
}

type e2eJSONRPCErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func e2eDoJSONRPC(t *testing.T, baseURL, method string, params map[string]any) e2eJSONRPCResp {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)
	resp, err := http.Post(baseURL+"/", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	var result e2eJSONRPCResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

// e2eKapeConfigLoader satisfies ports.KapeConfigLoader with fixed values.
type e2eKapeConfigLoader struct {
	cfg domainconfig.KapeConfig
}

var _ ports.KapeConfigLoader = &e2eKapeConfigLoader{}

func (l *e2eKapeConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return l.cfg, nil
}
