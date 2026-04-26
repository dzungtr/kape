package toml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	gotoml "github.com/pelletier/go-toml/v2"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// Renderer implements ports.TOMLRenderer.
type Renderer struct{}

// NewRenderer returns a new Renderer.
func NewRenderer() *Renderer { return &Renderer{} }

// Render serialises a KapeHandler, its resolved KapeSchema, resolved KapeTools,
// and platform config into a settings.toml string.
func (r *Renderer) Render(
	handler *v1alpha1.KapeHandler,
	schema *v1alpha1.KapeSchema,
	tools []v1alpha1.KapeTool,
	cfg domainconfig.KapeConfig,
) (string, error) {
	cfg = cfg.WithDefaults()

	replayOnStartup := true
	if handler.Spec.Trigger.ReplayOnStartup != nil {
		replayOnStartup = *handler.Spec.Trigger.ReplayOnStartup
	}
	maxIterations := handler.Spec.LLM.MaxIterations
	if maxIterations == 0 {
		maxIterations = cfg.DefaultMaxIterations
	}

	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")
	taskServiceEndpoint := fmt.Sprintf("http://kape-task-service.%s:8080", handler.Namespace)

	actions, err := buildActions(handler)
	if err != nil {
		return "", fmt.Errorf("building actions: %w", err)
	}

	toolSections := buildToolSections(handler, tools)

	schemaSection, err := buildSchemaSection(schema)
	if err != nil {
		return "", fmt.Errorf("building schema section: %w", err)
	}

	s := settingsTOML{
		Kape: kapeTOML{
			HandlerName:        handler.Name,
			HandlerNamespace:   handler.Namespace,
			ClusterName:        cfg.ClusterName,
			DryRun:             handler.Spec.DryRun,
			MaxIterations:      maxIterations,
			SchemaName:         handler.Spec.SchemaRef,
			ReplayOnStartup:    replayOnStartup,
			MaxEventAgeSeconds: handler.Spec.Trigger.MaxEventAgeSeconds,
		},
		LLM: llmTOML{
			Provider:     handler.Spec.LLM.Provider,
			Model:        handler.Spec.LLM.Model,
			SystemPrompt: handler.Spec.LLM.SystemPrompt,
		},
		NATS: natsTOML{
			Subject:  handler.Spec.Trigger.Type,
			Consumer: consumerName,
			Stream:   "kape-events",
		},
		TaskService: taskServiceTOML{Endpoint: taskServiceEndpoint},
		OTEL:        otelTOML{Endpoint: "http://otel-collector.kape-system:4318", ServiceName: "kape-handler"},
		Tools:       toolSections,
		Schema:      schemaSection,
		Actions:     actions,
	}

	var buf bytes.Buffer
	if err := gotoml.NewEncoder(&buf).Encode(s); err != nil {
		return "", fmt.Errorf("encoding settings.toml: %w", err)
	}
	return buf.String(), nil
}

func buildToolSections(handler *v1alpha1.KapeHandler, tools []v1alpha1.KapeTool) map[string]toolTOML {
	toolMap := make(map[string]v1alpha1.KapeTool, len(tools))
	for _, t := range tools {
		toolMap[t.Name] = t
	}
	result := make(map[string]toolTOML, len(handler.Spec.Tools))
	mcpPort := 8080
	for _, ref := range handler.Spec.Tools {
		t, ok := toolMap[ref.Ref]
		if !ok {
			continue
		}
		switch t.Spec.Type {
		case "mcp":
			result[ref.Ref] = toolTOML{
				Type:        "mcp",
				SidecarPort: mcpPort,
				Transport:   t.Spec.MCP.Upstream.Transport,
			}
			mcpPort++
		case "memory":
			result[ref.Ref] = toolTOML{
				Type:           "memory",
				QdrantEndpoint: t.Status.QdrantEndpoint,
			}
		}
	}
	return result
}

func buildSchemaSection(schema *v1alpha1.KapeSchema) (schemaTOML, error) {
	b, err := json.Marshal(schema.Spec.JSONSchema)
	if err != nil {
		return schemaTOML{}, fmt.Errorf("marshalling schema: %w", err)
	}
	return schemaTOML{JSON: string(b)}, nil
}

func buildActions(handler *v1alpha1.KapeHandler) ([]actionTOML, error) {
	result := make([]actionTOML, 0, len(handler.Spec.Actions))
	for _, a := range handler.Spec.Actions {
		data, err := convertActionData(a)
		if err != nil {
			return nil, fmt.Errorf("action %q: %w", a.Name, err)
		}
		result = append(result, actionTOML{
			Name:      a.Name,
			Condition: a.Condition,
			Type:      a.Type,
			Data:      data,
		})
	}
	return result, nil
}

func convertActionData(a v1alpha1.ActionSpec) (map[string]interface{}, error) {
	if len(a.Data) == 0 {
		return nil, nil
	}
	result := make(map[string]interface{}, len(a.Data))
	for k, v := range a.Data {
		var val interface{}
		if err := json.Unmarshal(v.Raw, &val); err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		result[k] = val
	}
	return result, nil
}

// ─── internal TOML struct tree ─────────────────────────────────────────────────

type settingsTOML struct {
	Kape        kapeTOML            `toml:"kape"`
	LLM         llmTOML             `toml:"llm"`
	NATS        natsTOML            `toml:"nats"`
	TaskService taskServiceTOML     `toml:"task_service"`
	OTEL        otelTOML            `toml:"otel"`
	Tools       map[string]toolTOML `toml:"tools,omitempty"`
	Schema      schemaTOML          `toml:"schema"`
	Actions     []actionTOML        `toml:"actions,omitempty"`
}

type kapeTOML struct {
	HandlerName        string `toml:"handler_name"`
	HandlerNamespace   string `toml:"handler_namespace"`
	ClusterName        string `toml:"cluster_name"`
	DryRun             bool   `toml:"dry_run"`
	MaxIterations      int32  `toml:"max_iterations"`
	SchemaName         string `toml:"schema_name"`
	ReplayOnStartup    bool   `toml:"replay_on_startup"`
	MaxEventAgeSeconds int64  `toml:"max_event_age_seconds"`
}

type llmTOML struct {
	Provider     string `toml:"provider"`
	Model        string `toml:"model"`
	SystemPrompt string `toml:"system_prompt"`
}

type natsTOML struct {
	Subject  string `toml:"subject"`
	Consumer string `toml:"consumer"`
	Stream   string `toml:"stream"`
}

type taskServiceTOML struct {
	Endpoint string `toml:"endpoint"`
}

type otelTOML struct {
	Endpoint    string `toml:"endpoint"`
	ServiceName string `toml:"service_name"`
}

type toolTOML struct {
	Type           string `toml:"type"`
	SidecarPort    int    `toml:"sidecar_port,omitempty"`
	Transport      string `toml:"transport,omitempty"`
	QdrantEndpoint string `toml:"qdrant_endpoint,omitempty"`
}

type schemaTOML struct {
	JSON string `toml:"json"`
}

type actionTOML struct {
	Name      string                 `toml:"name"`
	Condition string                 `toml:"condition"`
	Type      string                 `toml:"type"`
	Data      map[string]interface{} `toml:"data,omitempty"`
}
