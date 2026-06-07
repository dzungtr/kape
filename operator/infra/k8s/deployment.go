package k8s

import (
	"context"
	"fmt"
	"sort"

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

const handlerCertSecretName = "kape-handler-cert"

func deploymentName(handlerName string) string { return "kape-handler-" + handlerName }

// Ensure creates or patches the handler Deployment with a single kapeproxy sidecar.
func (a *DeploymentAdapter) Ensure(
	ctx context.Context,
	handler *v1alpha1.KapeHandler,
	cfg domainconfig.KapeConfig,
	rolloutHash string,
	tools []v1alpha1.KapeTool,
	lazySkillsPresent bool,
) error {
	name := deploymentName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}
	desired := buildDeployment(handler, cfg, rolloutHash, tools, lazySkillsPresent)

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

func buildDeployment(handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string, tools []v1alpha1.KapeTool, lazySkillsPresent bool) appsv1.Deployment {
	cfg = cfg.WithDefaults()
	name := deploymentName(handler.Name)
	saName := serviceAccountName(handler.Name)
	cmName := configMapName(handler.Name)
	kapeproxyCMName := kapeproxyConfigMapName(handler.Name)
	noAutoMount := false

	hasMCPTools := false
	for _, t := range tools {
		if t.Spec.Type == "mcp" {
			hasMCPTools = true
			break
		}
	}

	var replicas int32 = 1
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.MinReplicas > 0 {
		replicas = handler.Spec.Scaling.MinReplicas
	}

	envVars := []corev1.EnvVar{
		{Name: "KAPE_HANDLER_NAME", Value: handler.Name},
		{Name: "KAPE_NAMESPACE", Value: handler.Namespace},
	}
	envVars = append(envVars,
		corev1.EnvVar{Name: "NATS_TLS_CERT", Value: "/etc/kape/nats-certs/tls.crt"},
		corev1.EnvVar{Name: "NATS_TLS_KEY", Value: "/etc/kape/nats-certs/tls.key"},
		corev1.EnvVar{Name: "NATS_TLS_CA", Value: "/etc/kape/nats-certs/ca.crt"},
	)
	envVars = append(envVars, handler.Spec.Envs...)

	handlerVolumeMounts := []corev1.VolumeMount{{
		Name:      "settings",
		MountPath: "/etc/kape",
		ReadOnly:  true,
	}}
	handlerVolumeMounts = append(handlerVolumeMounts, corev1.VolumeMount{
		Name:      "nats-certs",
		MountPath: "/etc/kape/nats-certs",
		ReadOnly:  true,
	})
	volumes := []corev1.Volume{{
		Name: "settings",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
			},
		},
	}}
	volumes = append(volumes, corev1.Volume{
		Name: "nats-certs",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: handlerCertSecretName,
			},
		},
	})

	if lazySkillsPresent {
		handlerVolumeMounts = append(handlerVolumeMounts, corev1.VolumeMount{
			Name:      "kape-skills",
			MountPath: "/etc/kape/skills",
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "kape-skills",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: SkillConfigMapName(handler.Name)},
				},
			},
		})
	}

	// Collect memory-type tools and mount their connection Secrets as files.
	// Sort by Name for determinism when selecting KAPE_TOOL_NAME.
	var memoryTools []v1alpha1.KapeTool
	for _, t := range tools {
		if t.Spec.Type == "memory" {
			memoryTools = append(memoryTools, t)
		}
	}
	sort.Slice(memoryTools, func(i, j int) bool { return memoryTools[i].Name < memoryTools[j].Name })

	for _, mt := range memoryTools {
		secretName := "kape-tool-" + mt.Name + "-conn"
		volName := "kape-tool-" + mt.Name + "-secrets"
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		})
	}

	for _, mt := range memoryTools {
		volName := "kape-tool-" + mt.Name + "-secrets"
		mountPath := "/etc/kape/secrets/" + mt.Name
		handlerVolumeMounts = append(handlerVolumeMounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: mountPath,
			ReadOnly:  true,
		})
	}

	if len(memoryTools) > 0 {
		// TODO(#79): emit warning condition on KapeHandler.status when len(memoryTools) > 1.
		// For v1, the first tool's name (alphabetical — slice is sorted above) wins.
		envVars = append(envVars, corev1.EnvVar{
			Name:  "KAPE_TOOL_NAME",
			Value: memoryTools[0].Name,
		})
	}

	handlerContainer := corev1.Container{
		Name:         "handler",
		Image:        cfg.HandlerImageRef(),
		Env:          envVars,
		Resources:    resolveHandlerResources(handler.Spec.Resources),
		VolumeMounts: handlerVolumeMounts,
	}

	containers := []corev1.Container{handlerContainer}

	if hasMCPTools {
		volumes = append(volumes, corev1.Volume{
			Name: "kapeproxy-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: kapeproxyCMName},
				},
			},
		})
		containers = append(containers, buildKapeproxySidecar(cfg))
	}

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
					Volumes:                      volumes,
				},
			},
		},
	}
	setOwnerRef(handler, &dep.ObjectMeta)
	return dep
}

func buildKapeproxySidecar(cfg domainconfig.KapeConfig) corev1.Container {
	return corev1.Container{
		Name:  "kapeproxy",
		Image: cfg.KapeproxyImageRef(),
		Ports: []corev1.ContainerPort{{
			Name:          "mcp",
			ContainerPort: 8080,
			Protocol:      corev1.ProtocolTCP,
		}},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "kapeproxy-config",
			MountPath: "/etc/kapeproxy",
			ReadOnly:  true,
		}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
	}
}

func resolveHandlerResources(override *corev1.ResourceRequirements) corev1.ResourceRequirements {
	if override != nil {
		return *override
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}
