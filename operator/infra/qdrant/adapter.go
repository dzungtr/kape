// Package qdrant implements the QdrantClusterPort by delegating to the upstream
// qdrant-operator via its QdrantCluster CRD (qdrant.io/v1alpha1).
package qdrant

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

var qdrantClusterGVR = schema.GroupVersionResource{
	Group:    "qdrant.io",
	Version:  "v1alpha1",
	Resource: "qdrantclusters",
}

var qdrantClusterGVK = schema.GroupVersionKind{
	Group:   "qdrant.io",
	Version: "v1alpha1",
	Kind:    "QdrantCluster",
}

// QdrantClusterAdapter implements ports.QdrantClusterPort.
type QdrantClusterAdapter struct {
	client    client.Client
	discovery discovery.DiscoveryInterface
}

// NewQdrantClusterAdapter creates a new QdrantClusterAdapter.
func NewQdrantClusterAdapter(c client.Client, disc discovery.DiscoveryInterface) *QdrantClusterAdapter {
	return &QdrantClusterAdapter{client: c, discovery: disc}
}

// IsCRDInstalled returns true when QdrantCluster.qdrant.io is registered in the cluster.
func (a *QdrantClusterAdapter) IsCRDInstalled(ctx context.Context) (bool, error) {
	groups, err := a.discovery.ServerGroups()
	if err != nil {
		return false, fmt.Errorf("discovering API groups: %w", err)
	}
	for _, g := range groups.Groups {
		if g.Name == "qdrant.io" {
			for _, v := range g.Versions {
				if v.GroupVersion == "qdrant.io/v1alpha1" {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// EnsureQdrantCluster creates or updates a QdrantCluster owned by the KapeTool.
func (a *QdrantClusterAdapter) EnsureQdrantCluster(ctx context.Context, tool *v1alpha1.KapeTool) error {
	desired := buildQdrantCluster(tool)
	key := types.NamespacedName{Name: clusterName(tool.Name), Namespace: tool.Namespace}

	var existing unstructured.Unstructured
	existing.SetGroupVersionKind(qdrantClusterGVK)
	err := a.client.Get(ctx, key, &existing)
	if errors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting QdrantCluster %s/%s: %w", tool.Namespace, key.Name, err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	spec, _ := desired.Object["spec"].(map[string]interface{})
	if err := unstructured.SetNestedField(existing.Object, spec, "spec"); err != nil {
		return fmt.Errorf("merging QdrantCluster spec: %w", err)
	}
	return a.client.Patch(ctx, &existing, patch)
}

// GetQdrantClusterStatus reads readiness and connection URL from the upstream CRD status.
func (a *QdrantClusterAdapter) GetQdrantClusterStatus(ctx context.Context, key types.NamespacedName) (ready bool, connectionURL string, found bool, err error) {
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(qdrantClusterGVK)
	if err := a.client.Get(ctx, key, &obj); err != nil {
		if errors.IsNotFound(err) {
			return false, "", false, nil
		}
		return false, "", false, fmt.Errorf("getting QdrantCluster %s: %w", key, err)
	}

	// Check status.conditions for type=Ready, status=True.
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(cond, "type")
		condStatus, _, _ := unstructured.NestedString(cond, "status")
		if condType == "Ready" && condStatus == "True" {
			url, _, _ := unstructured.NestedString(obj.Object, "status", "url")
			if url == "" {
				// Fall back to a predictable in-cluster DNS name.
				url = fmt.Sprintf("http://%s.%s:6333", key.Name, key.Namespace)
			}
			return true, url, true, nil
		}
	}
	return false, "", true, nil
}

func clusterName(toolName string) string { return "kape-memory-" + toolName }

func buildQdrantCluster(tool *v1alpha1.KapeTool) unstructured.Unstructured {
	controller := true
	blockOwnerDeletion := true

	obj := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "qdrant.io/v1alpha1",
			"kind":       "QdrantCluster",
			"metadata": map[string]interface{}{
				"name":      clusterName(tool.Name),
				"namespace": tool.Namespace,
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         "kape.io/v1alpha1",
						"kind":               "KapeTool",
						"name":               tool.Name,
						"uid":                string(tool.UID),
						"controller":         controller,
						"blockOwnerDeletion": blockOwnerDeletion,
					},
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
			},
		},
	}
	obj.SetGroupVersionKind(qdrantClusterGVK)
	obj.SetCreationTimestamp(metav1.Time{})
	return obj
}
