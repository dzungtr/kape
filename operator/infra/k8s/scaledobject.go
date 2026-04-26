package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

var scaledObjectGVK = schema.GroupVersionKind{
	Group:   "keda.sh",
	Version: "v1alpha1",
	Kind:    "ScaledObject",
}

// ScaledObjectAdapter implements ports.ScaledObjectPort using unstructured resources.
// No kedacore/keda/v2 import is needed.
type ScaledObjectAdapter struct {
	client client.Client
}

// NewScaledObjectAdapter creates a new ScaledObjectAdapter.
func NewScaledObjectAdapter(c client.Client) *ScaledObjectAdapter {
	return &ScaledObjectAdapter{client: c}
}

// Ensure creates or patches the KEDA ScaledObject for the handler.
func (a *ScaledObjectAdapter) Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, consumerName string, cfg domainconfig.KapeConfig) error {
	cfg = cfg.WithDefaults()
	desired := buildScaledObject(handler, consumerName, cfg.NATSMonitoringEndpoint, cfg.NATSStreamName)
	key := client.ObjectKeyFromObject(desired)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(scaledObjectGVK)
	err := a.client.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("getting ScaledObject %s/%s: %w", handler.Namespace, desired.GetName(), err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Object["spec"] = desired.Object["spec"]
	return a.client.Patch(ctx, existing, patch)
}

// GetConsumerName reads the NATS consumer name from the existing ScaledObject.
func (a *ScaledObjectAdapter) GetConsumerName(ctx context.Context, key types.NamespacedName) (string, bool, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(scaledObjectGVK)
	if err := a.client.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("getting ScaledObject %s: %w", key, err)
	}
	triggers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "triggers")
	if len(triggers) == 0 {
		return "", true, nil
	}
	triggerMap, ok := triggers[0].(map[string]interface{})
	if !ok {
		return "", true, nil
	}
	consumer, _, _ := unstructured.NestedString(triggerMap, "metadata", "consumer")
	return consumer, true, nil
}

// Delete removes the ScaledObject. Returns nil when not found.
func (a *ScaledObjectAdapter) Delete(ctx context.Context, key types.NamespacedName) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(scaledObjectGVK)
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)
	if err := a.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting ScaledObject %s: %w", key, err)
	}
	return nil
}

func buildScaledObject(handler *v1alpha1.KapeHandler, consumerName, natsEndpoint, streamName string) *unstructured.Unstructured {
	scaling := resolveScaling(handler.Spec.Scaling)
	minReplicas := int64(scaling.MinReplicas)
	if scaling.ScaleToZero {
		minReplicas = 0
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "keda.sh/v1alpha1",
			"kind":       "ScaledObject",
			"metadata": map[string]interface{}{
				"name":      "kape-handler-" + handler.Name,
				"namespace": handler.Namespace,
			},
			"spec": map[string]interface{}{
				"scaleTargetRef":  map[string]interface{}{"name": "kape-handler-" + handler.Name},
				"minReplicaCount": minReplicas,
				"maxReplicaCount": int64(scaling.MaxReplicas),
				"cooldownPeriod":  int64(scaling.ScaleDownStabilizationSeconds),
				"triggers": []interface{}{
					map[string]interface{}{
						"type": "nats-jetstream",
						"metadata": map[string]interface{}{
							"natsServerMonitoringEndpoint": natsEndpoint,
							"streamName":                   streamName,
							"consumer":                     consumerName,
							"lagThreshold":                 fmt.Sprintf("%d", scaling.NatsLagThreshold),
						},
					},
				},
			},
		},
	}
	setHandlerOwnerRefUnstructured(handler, obj)
	return obj
}

func resolveScaling(s *v1alpha1.ScalingSpec) v1alpha1.ScalingSpec {
	if s == nil {
		return v1alpha1.ScalingSpec{MinReplicas: 1, MaxReplicas: 10, NatsLagThreshold: 5, ScaleDownStabilizationSeconds: 60}
	}
	out := *s
	if out.MaxReplicas == 0 {
		out.MaxReplicas = 10
	}
	if out.NatsLagThreshold == 0 {
		out.NatsLagThreshold = 5
	}
	if out.ScaleDownStabilizationSeconds == 0 {
		out.ScaleDownStabilizationSeconds = 60
	}
	if !out.ScaleToZero && out.MinReplicas == 0 {
		out.MinReplicas = 1
	}
	return out
}

// setHandlerOwnerRefUnstructured sets a KapeHandler owner reference on an unstructured object.
func setHandlerOwnerRefUnstructured(handler *v1alpha1.KapeHandler, obj *unstructured.Unstructured) {
	controller := true
	blockOwnerDeletion := true
	obj.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion:         "kape.io/v1alpha1",
		Kind:               "KapeHandler",
		Name:               handler.Name,
		UID:                handler.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}})
}
