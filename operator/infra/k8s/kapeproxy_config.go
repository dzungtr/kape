package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"gopkg.in/yaml.v3"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// KapeproxyConfigAdapter implements ports.KapeproxyConfigPort.
type KapeproxyConfigAdapter struct {
	client client.Client
}

// NewKapeproxyConfigAdapter creates a new KapeproxyConfigAdapter.
func NewKapeproxyConfigAdapter(c client.Client) *KapeproxyConfigAdapter {
	return &KapeproxyConfigAdapter{client: c}
}

func kapeproxyConfigMapName(handlerName string) string {
	return "kapeproxy-config-" + handlerName
}

// Ensure creates or updates the kapeproxy-config-{handler-name} ConfigMap.
func (a *KapeproxyConfigAdapter) Ensure(
	ctx context.Context,
	handler *v1alpha1.KapeHandler,
	tools []v1alpha1.KapeTool,
) error {
	rendered, err := RenderKapeproxyConfig(tools)
	if err != nil {
		return fmt.Errorf("rendering kapeproxy config: %w", err)
	}

	name := kapeproxyConfigMapName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}

	var cm corev1.ConfigMap
	err = a.client.Get(ctx, key, &cm)
	if apierrors.IsNotFound(err) {
		cm = buildKapeproxyConfigMap(handler, rendered)
		return a.client.Create(ctx, &cm)
	}
	if err != nil {
		return fmt.Errorf("getting ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}

	if cm.Data["config.yaml"] == rendered {
		return nil
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = map[string]string{"config.yaml": rendered}
	return a.client.Patch(ctx, &cm, patch)
}

func buildKapeproxyConfigMap(handler *v1alpha1.KapeHandler, yamlContent string) corev1.ConfigMap {
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kapeproxyConfigMapName(handler.Name),
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
			},
		},
		Data: map[string]string{"config.yaml": yamlContent},
	}
	setOwnerRef(handler, &cm.ObjectMeta)
	return cm
}

type upstreamEntry struct {
	URL          string          `yaml:"url"`
	Transport    string          `yaml:"transport"`
	AllowedTools []string        `yaml:"allowedTools,omitempty"`
	Redaction    *redactionEntry `yaml:"redaction,omitempty"`
	Audit        bool            `yaml:"audit"`
}

type redactionEntry struct {
	Input  []jsonPathEntry `yaml:"input,omitempty"`
	Output []jsonPathEntry `yaml:"output,omitempty"`
}

type jsonPathEntry struct {
	JSONPath string `yaml:"jsonPath"`
}

// RenderKapeproxyConfig produces the config.yaml content from mcp-type tools.
// tools must already be filtered to mcp type only.
func RenderKapeproxyConfig(tools []v1alpha1.KapeTool) (string, error) {
	sorted := make([]v1alpha1.KapeTool, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	upstreamsKey := &yaml.Node{Kind: yaml.ScalarNode, Value: "upstreams", Tag: "!!str"}
	upstreamsVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content, upstreamsKey, upstreamsVal)

	for _, t := range sorted {
		mcp := t.Spec.MCP
		if mcp == nil {
			continue
		}

		audit := true
		if mcp.Audit != nil && mcp.Audit.Enabled != nil {
			audit = *mcp.Audit.Enabled
		}

		entry := upstreamEntry{
			URL:       mcp.Upstream.URL,
			Transport: mcp.Upstream.Transport,
			Audit:     audit,
		}
		if len(mcp.AllowedTools) > 0 {
			entry.AllowedTools = mcp.AllowedTools
		}
		if mcp.Redaction != nil && (len(mcp.Redaction.Input) > 0 || len(mcp.Redaction.Output) > 0) {
			re := &redactionEntry{}
			for _, r := range mcp.Redaction.Input {
				re.Input = append(re.Input, jsonPathEntry{JSONPath: r.JSONPath})
			}
			for _, r := range mcp.Redaction.Output {
				re.Output = append(re.Output, jsonPathEntry{JSONPath: r.JSONPath})
			}
			entry.Redaction = re
		}

		entryNode, err := toYAMLNode(entry)
		if err != nil {
			return "", fmt.Errorf("encoding upstream %q: %w", t.Name, err)
		}

		nameNode := &yaml.Node{Kind: yaml.ScalarNode, Value: t.Name, Tag: "!!str"}
		upstreamsVal.Content = append(upstreamsVal.Content, nameNode, entryNode)
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshalling kapeproxy config: %w", err)
	}
	return string(b), nil
}

// toYAMLNode encodes a value via round-trip through yaml.Marshal → yaml.Unmarshal to get a *yaml.Node.
func toYAMLNode(v interface{}) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(b, &node); err != nil {
		return nil, err
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return node.Content[0], nil
	}
	return &node, nil
}
