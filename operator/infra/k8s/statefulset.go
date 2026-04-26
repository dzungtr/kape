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

// StatefulSetAdapter implements ports.StatefulSetPort.
type StatefulSetAdapter struct {
	client client.Client
}

// NewStatefulSetAdapter creates a new StatefulSetAdapter.
func NewStatefulSetAdapter(c client.Client) *StatefulSetAdapter {
	return &StatefulSetAdapter{client: c}
}

func qdrantName(toolName string) string { return "kape-memory-" + toolName }

// EnsureQdrant creates or patches the Qdrant StatefulSet and headless Service.
func (a *StatefulSetAdapter) EnsureQdrant(ctx context.Context, tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) error {
	cfg = cfg.WithDefaults()
	if err := a.ensureStatefulSet(ctx, tool, cfg); err != nil {
		return err
	}
	return a.ensureService(ctx, tool)
}

func (a *StatefulSetAdapter) ensureStatefulSet(ctx context.Context, tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) error {
	name := qdrantName(tool.Name)
	key := types.NamespacedName{Name: name, Namespace: tool.Namespace}
	desired := buildQdrantStatefulSet(tool, cfg)

	var existing appsv1.StatefulSet
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting StatefulSet %s/%s: %w", tool.Namespace, name, err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec.Template = desired.Spec.Template
	return a.client.Patch(ctx, &existing, patch)
}

func (a *StatefulSetAdapter) ensureService(ctx context.Context, tool *v1alpha1.KapeTool) error {
	name := qdrantName(tool.Name)
	key := types.NamespacedName{Name: name, Namespace: tool.Namespace}
	desired := buildQdrantService(tool)

	var existing corev1.Service
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting Service %s/%s: %w", tool.Namespace, name, err)
	}
	return nil // Service spec is immutable for ClusterIP=None; existence is sufficient
}

// GetQdrantReadyReplicas returns ready replica count. found=false when StatefulSet not found.
func (a *StatefulSetAdapter) GetQdrantReadyReplicas(ctx context.Context, key types.NamespacedName) (int32, bool, error) {
	var sts appsv1.StatefulSet
	if err := a.client.Get(ctx, key, &sts); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("getting StatefulSet %s: %w", key, err)
	}
	return sts.Status.ReadyReplicas, true, nil
}

func buildQdrantStatefulSet(tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) appsv1.StatefulSet {
	name := qdrantName(tool.Name)
	labels := map[string]string{"kape.io/qdrant": tool.Name}
	one := int32(1)
	storageClass := cfg.QdrantStorageClass

	sts := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tool.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name,
			Replicas:    &one,
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "qdrant",
						Image: "qdrant/qdrant:" + cfg.QdrantVersion,
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 6333, Protocol: corev1.ProtocolTCP},
							{Name: "grpc", ContainerPort: 6334, Protocol: corev1.ProtocolTCP},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "storage", MountPath: "/qdrant/storage"},
						},
					}},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "storage"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: &storageClass,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
			}},
		},
	}
	setToolOwnerRef(tool, &sts.ObjectMeta)
	return sts
}

func buildQdrantService(tool *v1alpha1.KapeTool) corev1.Service {
	name := qdrantName(tool.Name)
	labels := map[string]string{"kape.io/qdrant": tool.Name}
	svc := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tool.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  labels,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 6333, Protocol: corev1.ProtocolTCP},
				{Name: "grpc", Port: 6334, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	setToolOwnerRef(tool, &svc.ObjectMeta)
	return svc
}

// setToolOwnerRef sets a KapeTool owner reference on the given object.
func setToolOwnerRef(tool *v1alpha1.KapeTool, obj *metav1.ObjectMeta) {
	controller := true
	blockOwnerDeletion := true
	obj.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         "kape.io/v1alpha1",
		Kind:               "KapeTool",
		Name:               tool.Name,
		UID:                tool.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}}
}
