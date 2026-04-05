package k8s

import (
	"context"
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

// deploymentName returns the conventional Deployment name for a handler.
func deploymentName(handlerName string) string {
	return "kape-handler-" + handlerName
}

// Ensure creates or patches the handler Deployment.
func (a *DeploymentAdapter) Ensure(
	ctx context.Context,
	handler *v1alpha1.KapeHandler,
	cfg domainconfig.KapeConfig,
	rolloutHash string,
) error {
	name := deploymentName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}

	desired := buildDeployment(handler, cfg, rolloutHash)

	var existing appsv1.Deployment
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		if err := a.client.Create(ctx, &desired); err != nil {
			return fmt.Errorf("creating Deployment %s/%s: %w", handler.Namespace, name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting Deployment %s/%s: %w", handler.Namespace, name, err)
	}

	// Patch spec and annotations if rollout hash changed or image changed.
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec = desired.Spec
	existing.Annotations = desired.Annotations
	existing.Labels = desired.Labels
	if err := a.client.Patch(ctx, &existing, patch); err != nil {
		return fmt.Errorf("patching Deployment %s/%s: %w", handler.Namespace, name, err)
	}
	return nil
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

func buildDeployment(handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string) appsv1.Deployment {
	cfg = cfg.WithDefaults()
	name := deploymentName(handler.Name)
	saName := serviceAccountName(handler.Name)
	cmName := configMapName(handler.Name)

	var replicas int32 = 1
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.MinReplicas > 0 {
		replicas = handler.Spec.Scaling.MinReplicas
	}

	noAutoMount := false

	// Build env vars for the handler container.
	envVars := []corev1.EnvVar{
		{Name: "KAPE_HANDLER_NAME", Value: handler.Name},
		{Name: "KAPE_NAMESPACE", Value: handler.Namespace},
	}
	// Append operator-declared extra envs from spec.
	envVars = append(envVars, handler.Spec.Envs...)

	dep := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
				"app.kubernetes.io/name":       name,
			},
			Annotations: map[string]string{
				"kape.io/rollout-hash": rolloutHash,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kape.io/handler": handler.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kape.io/handler":        handler.Name,
						"app.kubernetes.io/name": name,
					},
					Annotations: map[string]string{
						"kape.io/rollout-hash": rolloutHash,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           saName,
					AutomountServiceAccountToken: &noAutoMount,
					Containers: []corev1.Container{
						{
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
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "settings",
									MountPath: "/etc/kape",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "settings",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
								},
							},
						},
					},
				},
			},
		},
	}
	setOwnerRef(handler, &dep.ObjectMeta)
	return dep
}
