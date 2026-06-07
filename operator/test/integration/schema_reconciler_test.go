//go:build envtest

package integration_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

var _ = Describe("SchemaReconciler", func() {
	const timeout = 10 * time.Second
	const poll = 500 * time.Millisecond

	Describe("valid schema", func() {
		It("sets Ready=True and writes schemaHash", func() {
			ns := uniqueNS("schema-valid")
			schema := readySchema("my-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())

			Eventually(func(g Gomega) {
				var s v1alpha1.KapeSchema
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "my-schema", Namespace: ns}, &s)).To(Succeed())
				c := findCondition(s.Status.Conditions, "Ready")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(c.Reason).To(Equal("Valid"))
				g.Expect(s.Status.SchemaHash).NotTo(BeEmpty())
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("invalid schema — required field not in properties", func() {
		It("sets Ready=False with reason InvalidSchema", func() {
			ns := uniqueNS("schema-invalid")
			addlPropsFalse := false
			// This schema has "missing-field" in required but it is NOT in properties.
			// The CRD admission passes (required and properties are both present),
			// but the reconciler's validateJSONSchema detects the mismatch.
			schema := &v1alpha1.KapeSchema{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-schema-req", Namespace: ns},
				Spec: v1alpha1.KapeSchemaSpec{
					Version: "v1",
					JSONSchema: v1alpha1.JSONSchemaObject{
						Type:     "object",
						Required: []string{"missing-field"},
						Properties: map[string]apiextensionsv1.JSON{
							"other-field": {Raw: []byte(`{"type":"string"}`)},
						},
						AdditionalProperties: &addlPropsFalse,
					},
				},
			}
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())

			Eventually(func(g Gomega) {
				var s v1alpha1.KapeSchema
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bad-schema-req", Namespace: ns}, &s)).To(Succeed())
				c := findCondition(s.Status.Conditions, "Ready")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(c.Reason).To(Equal("InvalidSchema"))
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("deletion blocked by live handler", func() {
		It("blocks schema deletion while a handler with schema-ref label exists", func() {
			ns := uniqueNS("schema-del-blocked")
			schema := readySchema("protected-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())

			// Wait for schema to be ready with its finalizer added.
			Eventually(func(g Gomega) {
				var s v1alpha1.KapeSchema
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "protected-schema", Namespace: ns}, &s)).To(Succeed())
				c := findCondition(s.Status.Conditions, "Ready")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, poll).Should(Succeed())

			// Create a handler referencing the schema; let the reconciler sync the schema-ref label.
			h := minimalHandler("blocking-handler", ns, "protected-schema")
			Expect(k8sClient.Create(ctx, h)).To(Succeed())

			// Wait for the handler reconciler to set kape.io/schema-ref label.
			Eventually(func(g Gomega) {
				var latest v1alpha1.KapeHandler
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "blocking-handler", Namespace: ns}, &latest)).To(Succeed())
				g.Expect(latest.Labels["kape.io/schema-ref"]).To(Equal("protected-schema"))
			}, timeout, poll).Should(Succeed())

			// Delete the schema — the finalizer should prevent immediate deletion.
			var s v1alpha1.KapeSchema
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "protected-schema", Namespace: ns}, &s)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &s)).To(Succeed())

			// Schema should still exist (DeletionTimestamp set) with Ready=False / ReferencedByHandlers.
			Eventually(func(g Gomega) {
				var latest v1alpha1.KapeSchema
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "protected-schema", Namespace: ns}, &latest)).To(Succeed())
				g.Expect(latest.DeletionTimestamp).NotTo(BeNil())
				c := findCondition(latest.Status.Conditions, "Ready")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(c.Reason).To(Equal("ReferencedByHandlers"))
			}, timeout, poll).Should(Succeed())
		})
	})

	Describe("schemaHash changes on spec update", func() {
		It("updates schemaHash when spec.version changes", func() {
			ns := uniqueNS("schema-hash")
			schema := readySchema("hash-schema", ns)
			Expect(k8sClient.Create(ctx, schema)).To(Succeed())

			var initialHash string
			Eventually(func(g Gomega) {
				var s v1alpha1.KapeSchema
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "hash-schema", Namespace: ns}, &s)).To(Succeed())
				g.Expect(s.Status.SchemaHash).NotTo(BeEmpty())
				initialHash = s.Status.SchemaHash
			}, timeout, poll).Should(Succeed())

			// Patch version to force a spec change.
			var s v1alpha1.KapeSchema
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "hash-schema", Namespace: ns}, &s)).To(Succeed())
			patch := client.MergeFrom(s.DeepCopy())
			s.Spec.Version = "v2"
			Expect(k8sClient.Patch(ctx, &s, patch)).To(Succeed())

			Eventually(func(g Gomega) {
				var latest v1alpha1.KapeSchema
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "hash-schema", Namespace: ns}, &latest)).To(Succeed())
				g.Expect(latest.Status.SchemaHash).NotTo(Equal(initialHash))
				g.Expect(latest.Status.SchemaHash).NotTo(BeEmpty())
			}, timeout, poll).Should(Succeed())
		})
	})
})
