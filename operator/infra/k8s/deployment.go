package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// DeploymentAdapter implements ports.DeploymentPort.
type DeploymentAdapter struct {
	client client.Client
}

// NewDeploymentAdapter creates a new DeploymentAdapter.
func NewDeploymentAdapter(c client.Client) *DeploymentAdapter {
	return &DeploymentAdapter{client: c}
}

func deploymentName(handlerName string) string { return "kape-handler-" + handlerName }

// Ensure creates or patches the handler Deployment with sidecar injection for mcp-type tools.
func (a *DeploymentAdapter) Ensure(
	ctx context.Context,
	handler *v1alpha1.KapeHandler,
	cfg domainconfig.KapeConfig,
	rolloutHash string,
	tools []v1alpha1.KapeTool,
) error {
	name := deploymentName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}
	desired := buildDeployment(handler, cfg, rolloutHash, tools)

	var existing appsv1.Deployment
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting Deployment %s/%s: %w", handler.Namespace, name, err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec = desired.Spec
	existing.Annotations = desired.Annotations
	existing.Labels = desired.Labels
	return a.client.Patch(ctx, &existing, patch)
}

// GetStatus reads the Deployment status. found is false when the Deployment does not exist.
func (a *DeploymentAdapter) GetStatus(ctx context.Context, key types.NamespacedName) (*appsv1.DeploymentStatus, bool, error) {
	var dep appsv1.Deployment
	if err := a.client.Get(ctx, key, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting Deployment %s: %w", key, err)
	}
	return &dep.Status, true, nil
}

func buildDeployment(handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string, tools []v1alpha1.KapeTool) appsv1.Deployment {
	cfg = cfg.WithDefaults()
	name := deploymentName(handler.Name)
	saName := serviceAccountName(handler.Name)
	cmName := configMapName(handler.Name)
	noAutoMount := false

	var replicas int32 = 1
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.MinReplicas > 0 {
		replicas = handler.Spec.Scaling.MinReplicas
	}

	envVars := []corev1.EnvVar{
		{Name: "KAPE_HANDLER_NAME", Value: handler.Name},
		{Name: "KAPE_NAMESPACE", Value: handler.Namespace},
	}
	envVars = append(envVars, handler.Spec.Envs...)

	handlerContainer := corev1.Container{
		Name:  "handler",
		Image: cfg.HandlerImageRef(),
		Env:   envVars,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "settings",
			MountPath: "/etc/kape",
			ReadOnly:  true,
		}},
	}

	containers := append([]corev1.Container{handlerContainer}, buildSidecars(handler, tools, cfg)...)

	dep := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
				"app.kubernetes.io/name":       name,
			},
			Annotations: map[string]string{"kape.io/rollout-hash": rolloutHash},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kape.io/handler": handler.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kape.io/handler":        handler.Name,
						"app.kubernetes.io/name": name,
					},
					Annotations: map[string]string{"kape.io/rollout-hash": rolloutHash},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           saName,
					AutomountServiceAccountToken: &noAutoMount,
					Containers:                   containers,
					Volumes: []corev1.Volume{{
						Name: "settings",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
							},
						},
					}},
				},
			},
		},
	}
	setOwnerRef(handler, &dep.ObjectMeta)
	return dep
}

func buildSidecars(handler *v1alpha1.KapeHandler, tools []v1alpha1.KapeTool, cfg domainconfig.KapeConfig) []corev1.Container {
	toolMap := make(map[string]v1alpha1.KapeTool, len(tools))
	for _, t := range tools {
		toolMap[t.Name] = t
	}

	var sidecars []corev1.Container
	sidecarPort := int32(8080)
	taskServiceEndpoint := fmt.Sprintf("http://kape-task-service.%s:8080", handler.Namespace)

	for _, ref := range handler.Spec.Tools {
		t, ok := toolMap[ref.Ref]
		if !ok || t.Spec.Type != "mcp" {
			continue
		}
		mcp := t.Spec.MCP

		auditEnabled := "true"
		if mcp.Audit != nil && mcp.Audit.Enabled != nil && !*mcp.Audit.Enabled {
			auditEnabled = "false"
		}

		allowedToolsJSON := "[]"
		if len(mcp.AllowedTools) > 0 {
			if b, err := json.Marshal(mcp.AllowedTools); err == nil {
				allowedToolsJSON = string(b)
			}
		}

		redactionInput, redactionOutput := "[]", "[]"
		if mcp.Redaction != nil {
			if b, err := json.Marshal(mcp.Redaction.Input); err == nil {
				redactionInput = string(b)
			}
			if b, err := json.Marshal(mcp.Redaction.Output); err == nil {
				redactionOutput = string(b)
			}
		}

		sidecars = append(sidecars, corev1.Container{
			Name:  "kapetool-" + ref.Ref,
			Image: cfg.KapetoolImageRef(),
			Ports: []corev1.ContainerPort{{
				Name:          "mcp",
				ContainerPort: sidecarPort,
				Protocol:      corev1.ProtocolTCP,
			}},
			Env: []corev1.EnvVar{
				{Name: "KAPETOOL_UPSTREAM_URL", Value: mcp.Upstream.URL},
				{Name: "KAPETOOL_UPSTREAM_TRANSPORT", Value: mcp.Upstream.Transport},
				{Name: "KAPETOOL_ALLOWED_TOOLS", Value: allowedToolsJSON},
				{Name: "KAPETOOL_REDACTION_INPUT", Value: redactionInput},
				{Name: "KAPETOOL_REDACTION_OUTPUT", Value: redactionOutput},
				{Name: "KAPETOOL_AUDIT_ENABLED", Value: auditEnabled},
				{Name: "KAPETOOL_TASK_SERVICE_ENDPOINT", Value: taskServiceEndpoint},
				{Name: "KAPETOOL_LISTEN_PORT", Value: fmt.Sprintf("%d", sidecarPort)},
			},
		})
		sidecarPort++
	}
	return sidecars
}
