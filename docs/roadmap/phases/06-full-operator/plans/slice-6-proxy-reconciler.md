# Slice 6 — KapeProxyReconciler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `KapeProxyReconciler` that watches Pods with label `kape.io/handler=*`, classifies the kapeproxy container's health, and writes `KapeProxyReady` / `KapeProxyDegraded` conditions on the parent `KapeHandler` — with no writes to any other resource.

**Architecture:** A new reconciler receives Pod events (filtered by label `kape.io/handler=*`), looks up the kapeproxy container status in the Pod, and calls a pure `evaluatePodHealth` function to classify the result into one of four states (`Ready`, `Degraded:RestartLoop`, `Degraded:ContainerNotReady`, `Missing`). The classification is then surfaced as two conditions (`KapeProxyReady`, `KapeProxyDegraded`) on the parent `KapeHandler` object via a `HandlerRepository.UpdateStatus` call. No Deployment or ConfigMap is touched.

**Tech Stack:** Go 1.25, controller-runtime v0.20+, `sigs.k8s.io/controller-runtime/pkg/client` (fake for unit, real for envtest), `github.com/stretchr/testify`, existing `ports.HandlerRepository`, existing `setCondition` helper in `operator/controller/reconcile/`.

---

## Acceptance criteria (from spec §1 Slice 6 and §3.2)

- Healthy kapeproxy → `KapeProxyReady=True` on the parent KapeHandler.
- CrashLoopBackOff kapeproxy → `KapeProxyReady=False`, `KapeProxyDegraded=True`, reason `RestartLoop`.
- Pod missing the kapeproxy container → `KapeProxyReady=False`, reason `KapeProxyMissing`.

---

## File map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `operator/infra/ports/pod.go` | `PodReader` port — read-only Pod listing |
| Create | `operator/infra/k8s/pod_reader.go` | `PodReaderAdapter` implementing `PodReader` |
| Create | `operator/controller/reconcile/kapeproxy.go` | `KapeProxyReconciler` + `evaluatePodHealth` |
| Create | `operator/controller/reconcile/kapeproxy_test.go` | Unit + fake-client tests |
| Create | `operator/controller/kapeproxy.go` | Controller wiring: `SetupKapeProxyReconciler` |
| Modify | `operator/cmd/main.go` | Register the new controller |

---

## Task 1: PodReader port interface

**Files:**
- Create: `operator/infra/ports/pod.go`

- [ ] **Step 1.1: Write the port file**

```go
package ports

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

// PodReader provides read-only access to Pod resources.
// Implementations must never mutate Pods.
type PodReader interface {
	// ListByLabel returns all Pods across all namespaces that carry the given label key/value.
	ListByLabel(ctx context.Context, labelKey, labelValue string) ([]corev1.Pod, error)

	// ListByHandlerName returns all Pods in the given namespace with label kape.io/handler=<name>.
	ListByHandlerName(ctx context.Context, namespace, handlerName string) ([]corev1.Pod, error)
}
```

- [ ] **Step 1.2: Verify the file compiles**

```bash
cd /path/to/repo/operator && go build ./infra/ports/...
```

Expected: no output (clean build).

- [ ] **Step 1.3: Commit**

```bash
git -C /path/to/repo add operator/infra/ports/pod.go
git -C /path/to/repo commit -m "feat(slice6): add PodReader port interface"
```

---

## Task 2: PodReaderAdapter (k8s adapter)

**Files:**
- Create: `operator/infra/k8s/pod_reader.go`

- [ ] **Step 2.1: Write the adapter**

```go
package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReaderAdapter implements ports.PodReader using a controller-runtime client.
type PodReaderAdapter struct {
	client client.Client
}

// NewPodReaderAdapter creates a PodReaderAdapter.
func NewPodReaderAdapter(c client.Client) *PodReaderAdapter {
	return &PodReaderAdapter{client: c}
}

// ListByLabel returns all Pods that carry the given label key/value across all namespaces.
func (a *PodReaderAdapter) ListByLabel(ctx context.Context, labelKey, labelValue string) ([]corev1.Pod, error) {
	var list corev1.PodList
	if err := a.client.List(ctx, &list, client.MatchingLabels{labelKey: labelValue}); err != nil {
		return nil, fmt.Errorf("listing pods by label %s=%s: %w", labelKey, labelValue, err)
	}
	return list.Items, nil
}

// ListByHandlerName returns all Pods in namespace with label kape.io/handler=<name>.
func (a *PodReaderAdapter) ListByHandlerName(ctx context.Context, namespace, handlerName string) ([]corev1.Pod, error) {
	var list corev1.PodList
	if err := a.client.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{"kape.io/handler": handlerName},
	); err != nil {
		return nil, fmt.Errorf("listing pods for handler %s/%s: %w", namespace, handlerName, err)
	}
	return list.Items, nil
}
```

- [ ] **Step 2.2: Verify it compiles**

```bash
cd /path/to/repo/operator && go build ./infra/k8s/...
```

Expected: no output.

- [ ] **Step 2.3: Commit**

```bash
git -C /path/to/repo add operator/infra/k8s/pod_reader.go
git -C /path/to/repo commit -m "feat(slice6): add PodReaderAdapter"
```

---

## Task 3: KapeProxyReconciler — core logic + pure function

**Files:**
- Create: `operator/controller/reconcile/kapeproxy.go`
- Create: `operator/controller/reconcile/kapeproxy_test.go`

### Background

The reconciler is triggered by a Pod event (via `ctrl.Request` carrying the Pod's namespaced name). It must:

1. Fetch the Pod.
2. Classify kapeproxy container health via `evaluatePodHealth`.
3. Look up the parent KapeHandler by the Pod's `kape.io/handler` label value.
4. Write `KapeProxyReady` and (when degraded) `KapeProxyDegraded` conditions on the KapeHandler.

### Health classification table (spec §3.2, §2.3)

| Pod condition | `evaluatePodHealth` result | `KapeProxyReady` | `KapeProxyDegraded` |
|---|---|---|---|
| kapeproxy container Ready=true, restarts < threshold | `proxyHealthy` | `True / Ready` | `False / Healthy` |
| kapeproxy container in CrashLoopBackOff | `proxyCrashLoop` | `False / ContainerCrashLoop` | `True / RestartLoop` |
| kapeproxy container exists but not ready (other reason) | `proxyNotReady` | `False / ContainerNotReady` | `False / Healthy` |
| kapeproxy container not found in pod spec/status | `proxyMissing` | `False / KapeProxyMissing` | not written |

**CrashLoopBackOff detection:** `containerStatus.State.Waiting != nil && containerStatus.State.Waiting.Reason == "CrashLoopBackOff"`.

- [ ] **Step 3.1: Write the failing unit tests first**

Create `operator/controller/reconcile/kapeproxy_test.go`:

```go
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
				{Name: "handler"}, // no kapeproxy container
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

	// KapeProxyDegraded must be False when healthy
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
```

- [ ] **Step 3.2: Run tests to verify they fail**

```bash
cd /path/to/repo/operator && go test ./controller/reconcile/... -run TestKapeProxy -v 2>&1 | head -20
```

Expected: compile error — `reconcile.NewKapeProxyReconciler`, `reconcile.EvaluatePodHealth`, `reconcile.ProxyHealthy` etc. are not yet defined.

- [ ] **Step 3.3: Write the reconciler implementation**

Create `operator/controller/reconcile/kapeproxy.go`:

```go
package reconcile

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/kape-io/kape/operator/infra/ports"
)

// ProxyHealth classifies the observed state of the kapeproxy container.
type ProxyHealth int

const (
	ProxyHealthy   ProxyHealth = iota // container is Running and Ready
	ProxyCrashLoop                    // container is in CrashLoopBackOff
	ProxyNotReady                     // container exists but not ready (other reason)
	ProxyMissing                      // no kapeproxy container found in Pod
)

const kapeproxyContainerName = "kapeproxy"

// KapeProxyReconciler watches Pods labelled kape.io/handler=* and writes
// KapeProxyReady / KapeProxyDegraded conditions on the parent KapeHandler.
// It is observability-only: it never mutates Deployments or ConfigMaps.
type KapeProxyReconciler struct {
	pods     ports.PodReader
	handlers ports.HandlerRepository
}

// NewKapeProxyReconciler creates a KapeProxyReconciler.
func NewKapeProxyReconciler(pods ports.PodReader, handlers ports.HandlerRepository) *KapeProxyReconciler {
	return &KapeProxyReconciler{pods: pods, handlers: handlers}
}

// Reconcile receives a Pod event, classifies kapeproxy health, and writes conditions
// on the parent KapeHandler.
func (r *KapeProxyReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("pod", key)

	// Fetch the triggering Pod
	pods, err := r.pods.ListByHandlerName(ctx, key.Namespace, "") // fetch by name below
	_ = pods
	// We need to fetch the specific pod by its name to resolve the handler label.
	// Use ListByLabel with a broader filter and find ours.
	allPods, err := r.pods.ListByLabel(ctx, "kape.io/handler", "")
	_ = allPods
	// The above approach lists too broadly. Instead, look up the specific pod.
	// PodReader.ListByHandlerName requires the handler name; we derive it from the Pod's label.
	// Fetch single pod via ListByLabel filtered by pod name is not supported by the port.
	// Use a direct get by adding GetPod to the port — but per the plan, PodReader has ListByLabel.
	// We derive the handler name from fetching all pods matching kape.io/handler=* in the namespace
	// and finding the one with matching pod name.
	podList, err := r.pods.ListByLabel(ctx, "kape.io/handler", "")
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing pods: %w", err)
	}

	// Find the specific pod
	var pod *corev1.Pod
	for i := range podList {
		if podList[i].Name == key.Name && podList[i].Namespace == key.Namespace {
			pod = &podList[i]
			break
		}
	}
	if pod == nil {
		// Pod deleted — nothing to do
		return ctrl.Result{}, nil
	}

	handlerName, ok := pod.Labels["kape.io/handler"]
	if !ok || handlerName == "" {
		log.V(1).Info("pod missing kape.io/handler label, skipping")
		return ctrl.Result{}, nil
	}

	health := EvaluatePodHealth(pod)

	handlerKey := types.NamespacedName{Name: handlerName, Namespace: pod.Namespace}
	handler, err := r.handlers.Get(ctx, handlerKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler %s: %w", handlerKey, err)
	}
	if handler == nil {
		log.V(1).Info("KapeHandler not found, skipping", "handler", handlerKey)
		return ctrl.Result{}, nil
	}

	handler.Status.Conditions = applyProxyConditions(handler.Status.Conditions, health)
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating KapeHandler status: %w", err)
	}

	return ctrl.Result{}, nil
}

// EvaluatePodHealth inspects the kapeproxy container status and returns a ProxyHealth value.
// Exported so it can be unit-tested independently of reconcile wiring.
func EvaluatePodHealth(pod *corev1.Pod) ProxyHealth {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != kapeproxyContainerName {
			continue
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return ProxyCrashLoop
		}
		if cs.Ready {
			return ProxyHealthy
		}
		return ProxyNotReady
	}
	return ProxyMissing
}

// applyProxyConditions sets KapeProxyReady and KapeProxyDegraded based on the health result.
func applyProxyConditions(conditions []metav1.Condition, health ProxyHealth) []metav1.Condition {
	switch health {
	case ProxyHealthy:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "kapeproxy container is running and ready",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionFalse,
			Reason:  "Healthy",
			Message: "kapeproxy container is healthy",
		})
	case ProxyCrashLoop:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ContainerCrashLoop",
			Message: "kapeproxy container is in CrashLoopBackOff",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionTrue,
			Reason:  "RestartLoop",
			Message: "kapeproxy container is crash-looping",
		})
	case ProxyNotReady:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ContainerNotReady",
			Message: "kapeproxy container exists but is not ready",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionFalse,
			Reason:  "Healthy",
			Message: "kapeproxy container is not yet ready",
		})
	case ProxyMissing:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "KapeProxyMissing",
			Message: "no kapeproxy container found in pod",
		})
		// KapeProxyDegraded is not written for Missing — the signal is absence, not degradation
	}
	return conditions
}
```

**Important note on the `ListByLabel` call with empty value:** The port's `ListByLabel` method uses `client.MatchingLabels{labelKey: labelValue}`. When `labelValue` is `""`, the controller-runtime fake client and real k8s API will match only pods where the label is literally set to `""`, not all pods with that label key. This is a bug in the approach above. Fix the port and adapter in Step 3.4 before running tests.

- [ ] **Step 3.4: Fix ListByLabel to support "key exists" semantics**

The reconciler needs to find *any* pod with the `kape.io/handler` label (regardless of value) by pod name+namespace. The cleaner design is to add a `GetPod` method to the port so the reconciler can fetch a single Pod by name:

Update `operator/infra/ports/pod.go` to add `GetPod`:

```go
package ports

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PodReader provides read-only access to Pod resources.
// Implementations must never mutate Pods.
type PodReader interface {
	// GetPod fetches a single Pod by namespaced name. Returns nil, nil when not found.
	GetPod(ctx context.Context, key types.NamespacedName) (*corev1.Pod, error)

	// ListByHandlerName returns all Pods in the given namespace with label kape.io/handler=<name>.
	ListByHandlerName(ctx context.Context, namespace, handlerName string) ([]corev1.Pod, error)
}
```

Update `operator/infra/k8s/pod_reader.go` to match:

```go
package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReaderAdapter implements ports.PodReader using a controller-runtime client.
type PodReaderAdapter struct {
	client client.Client
}

// NewPodReaderAdapter creates a PodReaderAdapter.
func NewPodReaderAdapter(c client.Client) *PodReaderAdapter {
	return &PodReaderAdapter{client: c}
}

// GetPod fetches a single Pod by namespaced name. Returns nil, nil when not found.
func (a *PodReaderAdapter) GetPod(ctx context.Context, key types.NamespacedName) (*corev1.Pod, error) {
	var pod corev1.Pod
	if err := a.client.Get(ctx, key, &pod); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &pod, nil
}

// ListByHandlerName returns all Pods in namespace with label kape.io/handler=<name>.
func (a *PodReaderAdapter) ListByHandlerName(ctx context.Context, namespace, handlerName string) ([]corev1.Pod, error) {
	var list corev1.PodList
	if err := a.client.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{"kape.io/handler": handlerName},
	); err != nil {
		return nil, fmt.Errorf("listing pods for handler %s/%s: %w", namespace, handlerName, err)
	}
	return list.Items, nil
}
```

Update `operator/controller/reconcile/kapeproxy.go` to use `GetPod`:

```go
package reconcile

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	corev1 "k8s.io/api/core/v1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// ProxyHealth classifies the observed state of the kapeproxy container.
type ProxyHealth int

const (
	ProxyHealthy   ProxyHealth = iota // container is Running and Ready
	ProxyCrashLoop                    // container is in CrashLoopBackOff
	ProxyNotReady                     // container exists but not ready (other reason)
	ProxyMissing                      // no kapeproxy container found in Pod
)

const kapeproxyContainerName = "kapeproxy"

// KapeProxyReconciler watches Pods labelled kape.io/handler=* and writes
// KapeProxyReady / KapeProxyDegraded conditions on the parent KapeHandler.
// It is observability-only: it never mutates Deployments or ConfigMaps.
type KapeProxyReconciler struct {
	pods     ports.PodReader
	handlers ports.HandlerRepository
}

// NewKapeProxyReconciler creates a KapeProxyReconciler.
func NewKapeProxyReconciler(pods ports.PodReader, handlers ports.HandlerRepository) *KapeProxyReconciler {
	return &KapeProxyReconciler{pods: pods, handlers: handlers}
}

// Reconcile receives a Pod event, classifies kapeproxy health, and writes conditions
// on the parent KapeHandler.
func (r *KapeProxyReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("pod", key)

	pod, err := r.pods.GetPod(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching pod %s: %w", key, err)
	}
	if pod == nil {
		return ctrl.Result{}, nil // Pod deleted
	}

	handlerName, ok := pod.Labels["kape.io/handler"]
	if !ok || handlerName == "" {
		log.V(1).Info("pod missing kape.io/handler label, skipping")
		return ctrl.Result{}, nil
	}

	health := EvaluatePodHealth(pod)

	handlerKey := types.NamespacedName{Name: handlerName, Namespace: pod.Namespace}
	handler, err := r.handlers.Get(ctx, handlerKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler %s: %w", handlerKey, err)
	}
	if handler == nil {
		log.V(1).Info("KapeHandler not found, skipping", "handler", handlerKey)
		return ctrl.Result{}, nil
	}

	handler.Status.Conditions = applyProxyConditions(handler.Status.Conditions, health)
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating KapeHandler status: %w", err)
	}

	return ctrl.Result{}, nil
}

// EvaluatePodHealth inspects the kapeproxy container status and returns a ProxyHealth value.
// Exported so it can be unit-tested independently of reconcile wiring.
func EvaluatePodHealth(pod *corev1.Pod) ProxyHealth {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != kapeproxyContainerName {
			continue
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return ProxyCrashLoop
		}
		if cs.Ready {
			return ProxyHealthy
		}
		return ProxyNotReady
	}
	return ProxyMissing
}

// applyProxyConditions sets KapeProxyReady and KapeProxyDegraded based on the health result.
func applyProxyConditions(conditions []metav1.Condition, health ProxyHealth) []metav1.Condition {
	switch health {
	case ProxyHealthy:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "kapeproxy container is running and ready",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionFalse,
			Reason:  "Healthy",
			Message: "kapeproxy container is healthy",
		})
	case ProxyCrashLoop:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ContainerCrashLoop",
			Message: "kapeproxy container is in CrashLoopBackOff",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionTrue,
			Reason:  "RestartLoop",
			Message: "kapeproxy container is crash-looping",
		})
	case ProxyNotReady:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ContainerNotReady",
			Message: "kapeproxy container exists but is not ready",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionFalse,
			Reason:  "Healthy",
			Message: "kapeproxy container is not yet ready",
		})
	case ProxyMissing:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "KapeProxyMissing",
			Message: "no kapeproxy container found in pod",
		})
		// KapeProxyDegraded is intentionally not written for Missing
	}
	return conditions
}
```

- [ ] **Step 3.5: Run tests to verify they pass**

```bash
cd /path/to/repo/operator && go test ./controller/reconcile/... -run TestKapeProxy -v
```

Expected output (all PASS):
```
--- PASS: TestKapeProxyReconciler_HealthyPod_SetsReadyTrue
--- PASS: TestKapeProxyReconciler_CrashLoop_SetsDegraded
--- PASS: TestKapeProxyReconciler_MissingContainer_SetsMissing
--- PASS: TestEvaluatePodHealth_Healthy
--- PASS: TestEvaluatePodHealth_CrashLoop
--- PASS: TestEvaluatePodHealth_NotReady
--- PASS: TestEvaluatePodHealth_Missing
```

- [ ] **Step 3.6: Run all reconcile tests to verify no regressions**

```bash
cd /path/to/repo/operator && go test ./controller/reconcile/... -v 2>&1 | tail -20
```

Expected: all PASS, no FAIL lines.

- [ ] **Step 3.7: Commit**

```bash
git -C /path/to/repo add \
  operator/infra/ports/pod.go \
  operator/infra/k8s/pod_reader.go \
  operator/controller/reconcile/kapeproxy.go \
  operator/controller/reconcile/kapeproxy_test.go
git -C /path/to/repo commit -m "feat(slice6): add KapeProxyReconciler with evaluatePodHealth + tests"
```

---

## Task 4: Controller wiring — SetupKapeProxyReconciler

**Files:**
- Create: `operator/controller/kapeproxy.go`

The controller wiring follows the same thin-adapter pattern as `operator/controller/schema.go` and `operator/controller/handler.go`. The reconciler is triggered by Pod events filtered to Pods with label `kape.io/handler` set to any non-empty value. We use `handler.EnqueueRequestsFromMapFunc` with a predicate that only enqueues when the label is present.

- [ ] **Step 4.1: Write the controller wiring**

```go
package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeProxyController is the thin controller-runtime adapter for the KapeProxyReconciler.
type KapeProxyController struct {
	inner *reconcilehandler.KapeProxyReconciler
}

// NewKapeProxyController creates a KapeProxyController.
func NewKapeProxyController(inner *reconcilehandler.KapeProxyReconciler) *KapeProxyController {
	return &KapeProxyController{inner: inner}
}

// Reconcile implements reconcile.Reconciler — delegates to the inner reconciler.
func (r *KapeProxyController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupKapeProxyReconciler registers the KapeProxy controller with the manager.
// It watches Pods with label kape.io/handler=* and enqueues them directly.
func SetupKapeProxyReconciler(mgr manager.Manager, inner *reconcilehandler.KapeProxyReconciler, maxConcurrent int) error {
	r := NewKapeProxyController(inner)
	return ctrl.NewControllerManagedBy(mgr).
		// Watch Pods; filter to only those with the kape.io/handler label.
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(mapHandlerPods)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}

// mapHandlerPods enqueues a reconcile request for each Pod that carries kape.io/handler label.
// Pods without the label are silently ignored.
func mapHandlerPods(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	if labels == nil {
		return nil
	}
	if _, ok := labels["kape.io/handler"]; !ok {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}},
	}
}
```

- [ ] **Step 4.2: Verify compilation**

```bash
cd /path/to/repo/operator && go build ./controller/...
```

Expected: no output.

- [ ] **Step 4.3: Commit**

```bash
git -C /path/to/repo add operator/controller/kapeproxy.go
git -C /path/to/repo commit -m "feat(slice6): add KapeProxyController wiring"
```

---

## Task 5: Register in operator/cmd/main.go

**Files:**
- Modify: `operator/cmd/main.go`

- [ ] **Step 5.1: Add PodReaderAdapter instantiation and register the controller**

Find the section in `main.go` after the existing adapter declarations (around line 94) and add:

```go
// After the existing adapter declarations:
podReader := k8sadapters.NewPodReaderAdapter(k8sClient)

// KapeProxyReconciler
proxyRec := reconcilehandler.NewKapeProxyReconciler(podReader, handlerRepo)
if err := kapecontroller.SetupKapeProxyReconciler(mgr, proxyRec, cfg.MaxConcurrentReconciles); err != nil {
    log.Error(err, "setting up KapeProxy controller")
    os.Exit(1)
}
```

The full updated `main.go` adapter block (lines 82-128) should look like:

```go
k8sClient := mgr.GetClient()

// Adapters
handlerRepo     := k8sadapters.NewHandlerRepository(k8sClient)
schemaRepo      := k8sadapters.NewSchemaRepository(k8sClient)
toolRepo        := k8sadapters.NewToolRepository(k8sClient)
configMapAdapt  := k8sadapters.NewConfigMapAdapter(k8sClient)
saAdapt         := k8sadapters.NewServiceAccountAdapter(k8sClient)
deployAdapt     := k8sadapters.NewDeploymentAdapter(k8sClient)
scaledObjAdapt  := k8sadapters.NewScaledObjectAdapter(k8sClient)
cfgLoader       := k8sadapters.NewKapeConfigLoader(k8sClient, cfg.KapeConfigNamespace, cfg.KapeConfigName)
renderer        := tomlrenderer.NewRenderer()
statefulSetAdapt := k8sadapters.NewStatefulSetAdapter(k8sClient)
podReader        := k8sadapters.NewPodReaderAdapter(k8sClient)

// KapeToolReconciler
toolRec := reconcilehandler.NewToolReconciler(toolRepo, statefulSetAdapt, cfgLoader)
if err := kapecontroller.SetupToolReconciler(mgr, toolRec, cfg.MaxConcurrentReconciles); err != nil {
    log.Error(err, "setting up KapeTool controller")
    os.Exit(1)
}

// KapeSchemaReconciler
schemaRec := reconcilehandler.NewSchemaReconciler(schemaRepo)
if err := kapecontroller.SetupSchemaReconciler(mgr, schemaRec, cfg.MaxConcurrentReconciles); err != nil {
    log.Error(err, "setting up KapeSchema controller")
    os.Exit(1)
}

// KapeHandlerReconciler
handlerRec := reconcilehandler.NewHandlerReconciler(
    handlerRepo,
    schemaRepo,
    toolRepo,
    configMapAdapt,
    saAdapt,
    deployAdapt,
    scaledObjAdapt,
    renderer,
    cfgLoader,
)
if err := kapecontroller.SetupHandlerReconciler(mgr, handlerRec, cfg.MaxConcurrentReconciles); err != nil {
    log.Error(err, "setting up KapeHandler controller")
    os.Exit(1)
}

// KapeProxyReconciler
proxyRec := reconcilehandler.NewKapeProxyReconciler(podReader, handlerRepo)
if err := kapecontroller.SetupKapeProxyReconciler(mgr, proxyRec, cfg.MaxConcurrentReconciles); err != nil {
    log.Error(err, "setting up KapeProxy controller")
    os.Exit(1)
}
```

- [ ] **Step 5.2: Verify full build**

```bash
cd /path/to/repo/operator && go build ./...
```

Expected: no output.

- [ ] **Step 5.3: Run full test suite to verify no regressions**

```bash
cd /path/to/repo/operator && go test ./... 2>&1 | tail -20
```

Expected: all PASS, no FAIL.

- [ ] **Step 5.4: Commit**

```bash
git -C /path/to/repo add operator/cmd/main.go
git -C /path/to/repo commit -m "feat(slice6): register KapeProxyReconciler in main"
```

---

## Task 6: Snyk Code Scan

**Context:** Per kape-io CLAUDE.md and the global user instructions, a Snyk Code scan must be run on `operator/` before raising a PR. Use the MCP tool — never shell out to the Snyk CLI.

- [ ] **Step 6.1: Run Snyk Code scan on operator/**

Call `mcp__Snyk__snyk_code_scan` with path `operator/`.

- [ ] **Step 6.2: Fix any high/critical issues**

If Snyk reports security issues in the newly introduced files:
- `operator/infra/ports/pod.go`
- `operator/infra/k8s/pod_reader.go`
- `operator/controller/reconcile/kapeproxy.go`
- `operator/controller/kapeproxy.go`

Fix each issue, re-run `go test ./...`, commit the fix, then re-scan.

- [ ] **Step 6.3: Re-scan until clean**

Repeat Step 6.1 after each fix. Proceed to Task 7 only when the scan returns no new issues in the files introduced by this slice.

---

## Task 7: SBOM Scans (per kape-io CLAUDE.md)

Per the kape-io PR checklist, three SBOM scans must run before creating the PR.

- [ ] **Step 7.1: Run SBOM scan on ./adapters**

Call `mcp__Snyk__snyk_sbom_scan` with:
- `path`: `./adapters`
- `format`: `cyclonedx1.4+json`

Record the component count and any flagged components.

- [ ] **Step 7.2: Run SBOM scan on ./operator**

Call `mcp__Snyk__snyk_sbom_scan` with:
- `path`: `./operator`
- `format`: `cyclonedx1.4+json`

Record the component count and any flagged components.

- [ ] **Step 7.3: Run SBOM scan on ./task-service**

Call `mcp__Snyk__snyk_sbom_scan` with:
- `path`: `./task-service`
- `format`: `cyclonedx1.4+json`

Record the component count and any flagged components.

---

## Task 8: Push and open PR

- [ ] **Step 8.1: Use `superpowers:finishing-a-development-branch`**

Invoke the skill to verify tests pass, push the branch, and open a PR with:
- Title: `feat(phase6): KapeProxyReconciler — observability-only (slice 6)`
- Base: `main`

- [ ] **Step 8.2: Post SBOM summary comment on the PR**

After the PR is open, post a single comment using:

```bash
gh pr comment "$(gh pr view --json url --jq '.url')" --body "$(cat <<'EOF'
## SBOM Summary

| Module | Components | Flagged |
|---|---|---|
| adapters | <count from Task 7.1> | <count or "none"> |
| operator | <count from Task 7.2> | <count or "count"> |
| task-service | <count from Task 7.3> | <count or "none"> |

Generated via Snyk CycloneDX 1.4 — <insert current UTC ISO-8601 timestamp here>
EOF
)"
```

Replace `<count from Task 7.x>` with actual values from the scans. If a scan failed, write `FAILED: <error>` in Components and `N/A` in Flagged.

---

## Self-review against spec

| Spec requirement | Covered by |
|---|---|
| §1 Slice 6: `operator/infra/ports/pod.go` — `PodReader` port | Task 1 + Task 3.4 (updated) |
| §1 Slice 6: `operator/infra/k8s/pod_reader.go` — adapter | Task 2 + Task 3.4 (updated) |
| §1 Slice 6: `operator/controller/reconcile/kapeproxy.go` | Task 3 |
| §1 Slice 6: `operator/controller/kapeproxy.go` — wiring | Task 4 |
| §1 Slice 6: `operator/cmd/main.go` registration | Task 5 |
| §2.3: `KapeProxyReady` conditions — `Ready`, `ContainerCrashLoop`, `ContainerNotReady`, `KapeProxyMissing` | `applyProxyConditions` in Task 3.4 |
| §2.3: `KapeProxyDegraded` conditions — `RestartLoop`, `Healthy` | `applyProxyConditions` in Task 3.4 |
| §3.2 Slice 6: `evaluatePodHealth` unit test | `TestEvaluatePodHealth_*` in Task 3.1 |
| §3.2 Slice 6: envtest Healthy → `KapeProxyReady=True` | `TestKapeProxyReconciler_HealthyPod_SetsReadyTrue` in Task 3.1 |
| §3.2 Slice 6: envtest CrashLoopBackOff → `Ready=False, Degraded=True, RestartLoop` | `TestKapeProxyReconciler_CrashLoop_SetsDegraded` in Task 3.1 |
| §3.2 Slice 6: envtest missing container → `Missing` | `TestKapeProxyReconciler_MissingContainer_SetsMissing` in Task 3.1 |
| D12: Slice 6 does NOT write kapeproxy-config ConfigMap | `KapeProxyReconciler` only calls `handlers.UpdateStatus` |
| D15: KapeProxyDegraded set only on Pod-level signals | `evaluatePodHealth` uses only `ContainerStatuses`, no log scraping |
| R5: Coarse signal accepted for M2 | Documented in spec; no Phase 6 gap to close |
| CLAUDE.md: Snyk Code scan on operator/ | Task 6 |
| CLAUDE.md: SBOM scans on all 3 modules + PR comment | Task 7 + Task 8.2 |
| DoD: All 3 scenarios tested | Tasks 3.1 tests cover all three |
