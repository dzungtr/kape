package reconcile_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// ─── scheme ──────────────────────────────────────────────────────────────────

func newProxyScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func baseHandler(name, ns string) *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
}

func podWithKapeproxy(name, ns, handlerName string, containerStatus corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"kape.io/handler": handlerName},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{containerStatus},
		},
	}
}

func podWithoutKapeproxy(name, ns, handlerName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"kape.io/handler": handlerName},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "handler"},
			},
		},
	}
}

func healthyContainerStatus() corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  "kapeproxy",
		Ready: true,
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{},
		},
	}
}

func crashLoopContainerStatus() corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:         "kapeproxy",
		Ready:        false,
		RestartCount: 5,
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{
				Reason: "CrashLoopBackOff",
			},
		},
	}
}

func notReadyContainerStatus() corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  "kapeproxy",
		Ready: false,
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{
				Reason: "ContainerCreating",
			},
		},
	}
}

// ─── DoD scenario 1: Healthy kapeproxy → KapeProxyReady=True ─────────────────

func TestKapeProxyReconciler_HealthyPod_SetsReadyTrue(t *testing.T) {
	s := newProxyScheme()
	handler := baseHandler("my-handler", "kape-system")
	pod := podWithKapeproxy("my-handler-pod-abc", "kape-system", "my-handler", healthyContainerStatus())

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, pod).
		WithStatusSubresource(handler).
		Build()

	r := reconcile.NewKapeProxyReconciler(
		k8sadapters.NewPodReaderAdapter(c),
		k8sadapters.NewHandlerRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(),
		types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})
	readyCond := findCondition(got.Status.Conditions, "KapeProxyReady")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
	assert.Equal(t, "Ready", readyCond.Reason)

	degradedCond := findCondition(got.Status.Conditions, "KapeProxyDegraded")
	require.NotNil(t, degradedCond)
	assert.Equal(t, metav1.ConditionFalse, degradedCond.Status)
	assert.Equal(t, "Healthy", degradedCond.Reason)
}

// ─── DoD scenario 2: CrashLoopBackOff → KapeProxyReady=False, KapeProxyDegraded=True ──

func TestKapeProxyReconciler_CrashLoop_SetsDegraded(t *testing.T) {
	s := newProxyScheme()
	handler := baseHandler("my-handler", "kape-system")
	pod := podWithKapeproxy("my-handler-pod-abc", "kape-system", "my-handler", crashLoopContainerStatus())

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, pod).
		WithStatusSubresource(handler).
		Build()

	r := reconcile.NewKapeProxyReconciler(
		k8sadapters.NewPodReaderAdapter(c),
		k8sadapters.NewHandlerRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(),
		types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	readyCond := findCondition(got.Status.Conditions, "KapeProxyReady")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
	assert.Equal(t, "ContainerCrashLoop", readyCond.Reason)

	degradedCond := findCondition(got.Status.Conditions, "KapeProxyDegraded")
	require.NotNil(t, degradedCond)
	assert.Equal(t, metav1.ConditionTrue, degradedCond.Status)
	assert.Equal(t, "RestartLoop", degradedCond.Reason)
}

// ─── DoD scenario 3: Pod missing kapeproxy container → KapeProxyReady=False, reason KapeProxyMissing ──

func TestKapeProxyReconciler_MissingContainer_SetsMissing(t *testing.T) {
	s := newProxyScheme()
	handler := baseHandler("my-handler", "kape-system")
	pod := podWithoutKapeproxy("my-handler-pod-abc", "kape-system", "my-handler")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, pod).
		WithStatusSubresource(handler).
		Build()

	r := reconcile.NewKapeProxyReconciler(
		k8sadapters.NewPodReaderAdapter(c),
		k8sadapters.NewHandlerRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(),
		types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	readyCond := findCondition(got.Status.Conditions, "KapeProxyReady")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
	assert.Equal(t, "KapeProxyMissing", readyCond.Reason)
}

// ─── Unit test: evaluatePodHealth pure function ───────────────────────────────

func TestEvaluatePodHealth_Healthy(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{healthyContainerStatus()},
		},
	}
	result := reconcile.EvaluatePodHealth(pod)
	assert.Equal(t, reconcile.ProxyHealthy, result)
}

func TestEvaluatePodHealth_CrashLoop(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{crashLoopContainerStatus()},
		},
	}
	result := reconcile.EvaluatePodHealth(pod)
	assert.Equal(t, reconcile.ProxyCrashLoop, result)
}

func TestEvaluatePodHealth_NotReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{notReadyContainerStatus()},
		},
	}
	result := reconcile.EvaluatePodHealth(pod)
	assert.Equal(t, reconcile.ProxyNotReady, result)
}

func TestEvaluatePodHealth_Missing(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "handler", Ready: true},
			},
		},
	}
	result := reconcile.EvaluatePodHealth(pod)
	assert.Equal(t, reconcile.ProxyMissing, result)
}
