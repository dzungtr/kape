//go:build envtest

package integration_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

var _ = Describe("HandlerReconciler", func() {
	const timeout = 15 * time.Second
	const poll = 500 * time.Millisecond
	// longTimeout covers the 30s requeue used when dependencies are not ready.
	const longTimeout = 50 * time.Second

	// waitForSchemaReady waits for a KapeSchema to reach Ready=True.
	waitForSchemaReady := func(name, ns string) {
		Eventually(func(g Gomega) {
			var s v1alpha1.KapeSchema
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, &s)).To(Succeed())
			c := findCondition(s.Status.Conditions, "Ready")
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
		}, timeout, poll).Should(Succeed(), "schema %s should be ready", name)
	}

	// waitForToolReady waits for a KapeTool to reach Ready=True.
	waitForToolReady := func(name, ns string) {
		Eventually(func(g Gomega) {
			var t v1alpha1.KapeTool
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, &t)).To(Succeed())
			c := findCondition(t.Status.Conditions, "Ready")
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
		}, timeout, poll).Should(Succeed(), "tool %s should be ready", name)
	}

	Describe("dependency gate — schema not ready", func() {
		It("sets DependenciesReady=False/KapeSchemaInvalid and requeueing", func() {
			ns := uniqueNS("handler-gate-schema")
			// Create handler before schema exists.
			h := minimalHandler("my-handler", ns, "nonexistent-schema")
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			Eventually(func(g Gomega) {
				var latest v1alpha1.KapeHandler
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "my-handler", Namespace: ns}, &latest)).To(Succeed())
				c := findCondition(latest.Status.Conditions, "DependenciesReady")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(c.Reason).To(Equal("KapeSchemaInvalid"))
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("happy-path object graph", func() {
		It("creates Deployment, ConfigMap, and ServiceAccount after schema is ready", func() {
			ns := uniqueNS("handler-happy")
			schema := readySchema("alert-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())
			waitForSchemaReady("alert-schema", ns)

			h := minimalHandler("alert-handler", ns, "alert-schema")
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			depKey := types.NamespacedName{Name: "kape-handler-alert-handler", Namespace: ns}

			Eventually(func(g Gomega) {
				var dep appsv1.Deployment
				g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
				g.Expect(dep.Annotations["kape.io/rollout-hash"]).NotTo(BeEmpty())
			}, timeout, poll).Should(Succeed())

			// ConfigMap
			Eventually(func(g Gomega) {
				var cm corev1.ConfigMap
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kape-handler-alert-handler", Namespace: ns}, &cm)).To(Succeed())
				g.Expect(cm.Data["settings.toml"]).NotTo(BeEmpty())
			}, timeout, poll).Should(Succeed())

			// ServiceAccount
			Eventually(func(g Gomega) {
				var sa corev1.ServiceAccount
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kape-handler-alert-handler", Namespace: ns}, &sa)).To(Succeed())
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("label propagation", func() {
		It("syncs schema-ref and tool-ref labels after reconcile", func() {
			ns := uniqueNS("handler-labels")
			schema := readySchema("label-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())
			waitForSchemaReady("label-schema", ns)

			tool := eventPublishTool("label-tool", ns)
			Expect(k8sClient.Create(ctx, tool)).To(Succeed())
			waitForToolReady("label-tool", ns)

			h := minimalHandler("label-handler", ns, "label-schema")
			h.Spec.Tools = []v1alpha1.ToolRef{{Ref: "label-tool"}}
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			Eventually(func(g Gomega) {
				var latest v1alpha1.KapeHandler
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "label-handler", Namespace: ns}, &latest)).To(Succeed())
				g.Expect(latest.Labels["kape.io/schema-ref"]).To(Equal("label-schema"))
				g.Expect(latest.Labels["kape.io/tool-ref-label-tool"]).To(Equal("true"))
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("rollout-hash propagation on schema update", func() {
		It("changes rollout-hash annotation when schema spec changes", func() {
			ns := uniqueNS("handler-hash-schema")
			schema := readySchema("hash-trigger-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())
			waitForSchemaReady("hash-trigger-schema", ns)

			h := minimalHandler("hash-handler", ns, "hash-trigger-schema")
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			depKey := types.NamespacedName{Name: "kape-handler-hash-handler", Namespace: ns}
			var initialHash string

			Eventually(func(g Gomega) {
				var dep appsv1.Deployment
				g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
				g.Expect(dep.Annotations["kape.io/rollout-hash"]).NotTo(BeEmpty())
				initialHash = dep.Annotations["kape.io/rollout-hash"]
			}, timeout, poll).Should(Succeed())

			// Patch schema spec to change the hash.
			var s v1alpha1.KapeSchema
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "hash-trigger-schema", Namespace: ns}, &s)).To(Succeed())
			patch := client.MergeFrom(s.DeepCopy())
			s.Spec.Version = "v2"
			Expect(k8sClient.Patch(ctx, &s, patch)).To(Succeed())

			Eventually(func(g Gomega) {
				var dep appsv1.Deployment
				g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
				newHash := dep.Annotations["kape.io/rollout-hash"]
				g.Expect(newHash).NotTo(BeEmpty())
				g.Expect(newHash).NotTo(Equal(initialHash))
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("scaling validation", func() {
		It("sets ScalingConfigured=False when scaleToZero=true but minReplicas>0", func() {
			ns := uniqueNS("handler-scaling")
			schema := readySchema("scale-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())
			waitForSchemaReady("scale-schema", ns)

			h := minimalHandler("scale-handler", ns, "scale-schema")
			h.Spec.Scaling = &v1alpha1.ScalingSpec{
				MinReplicas: 1,
				MaxReplicas: 10,
				ScaleToZero: true,
			}
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			Eventually(func(g Gomega) {
				var latest v1alpha1.KapeHandler
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "scale-handler", Namespace: ns}, &latest)).To(Succeed())
				c := findCondition(latest.Status.Conditions, "ScalingConfigured")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(c.Reason).To(Equal("InvalidScalingConfig"))
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("gate clearing — schema becomes ready after handler creation", func() {
		It("transitions from DependenciesReady=False to Deployment created once schema is ready", func() {
			ns := uniqueNS("handler-gate-clear")
			// Create handler with a schema that doesn't exist yet.
			h := minimalHandler("gate-clear-handler", ns, "late-schema")
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			// Wait for gate block.
			Eventually(func(g Gomega) {
				var latest v1alpha1.KapeHandler
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "gate-clear-handler", Namespace: ns}, &latest)).To(Succeed())
				c := findCondition(latest.Status.Conditions, "DependenciesReady")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			}, timeout, poll).Should(Succeed())

			// Now create the schema.
			schema := readySchema("late-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())
			waitForSchemaReady("late-schema", ns)

			// Handler will be requeued after 30s (RequeueAfter from dependency gate).
			// longTimeout is set above the requeue interval.
			depKey := types.NamespacedName{Name: "kape-handler-gate-clear-handler", Namespace: ns}
			Eventually(func(g Gomega) {
				var dep appsv1.Deployment
				g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
				g.Expect(dep.Annotations["kape.io/rollout-hash"]).NotTo(BeEmpty())
			}, longTimeout, poll).Should(Succeed())
		})
	})

	Describe("KEDA ScaledObject creation", func() {
		It("creates the handler Deployment even when KEDA ScaledObject is stubbed", func() {
			ns := uniqueNS("handler-keda")
			schema := readySchema("keda-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())
			waitForSchemaReady("keda-schema", ns)

			h := minimalHandler("keda-handler", ns, "keda-schema")
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			depKey := types.NamespacedName{Name: "kape-handler-keda-handler", Namespace: ns}
			Eventually(func(g Gomega) {
				var dep appsv1.Deployment
				g.Expect(k8sClient.Get(ctx, depKey, &dep)).To(Succeed())
			}, timeout, poll).Should(Succeed())
		})
	})
})
