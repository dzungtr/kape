package k8s_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/k8s"
)

// kape#274 (ADR-0083 D5): the operator owns the controller→daemon bearer token.
func TestDaemonTokenStore_GeneratesOnceAndReuses(t *testing.T) {
	server := &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "kape-system", UID: "uid-1"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(server).Build()
	store := k8s.NewDaemonTokenStore(c)

	name, err := store.Ensure(context.Background(), server)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if name != k8s.DaemonTokenSecretName("default") {
		t.Fatalf("secret name = %q", name)
	}
	var secret corev1.Secret
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: name}, &secret); err != nil {
		t.Fatalf("reading created Secret: %v", err)
	}
	first := string(secret.Data[k8s.DaemonTokenSecretKey])
	if len(first) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars", len(first))
	}
	if len(secret.OwnerReferences) != 1 {
		t.Fatalf("owner references = %d, want 1 (GC with the CR)", len(secret.OwnerReferences))
	}

	// Idempotent: a second reconcile must NOT rotate a token that running
	// sandboxes already hold.
	name2, err := store.Ensure(context.Background(), server)
	if err != nil {
		t.Fatalf("Ensure (second): %v", err)
	}
	var again corev1.Secret
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: name2}, &again); err != nil {
		t.Fatalf("re-reading Secret: %v", err)
	}
	if got := string(again.Data[k8s.DaemonTokenSecretKey]); got != first {
		t.Fatal("token regenerated on an existing Secret, want stable")
	}
}

// An existing but empty token is repaired, not accepted: a tokenless Secret
// would 401 every controller→daemon request with nothing in the UI to explain it.
func TestDaemonTokenStore_RepairsEmptyToken(t *testing.T) {
	server := &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "kape-system", UID: "uid-1"},
	}
	empty := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: k8s.DaemonTokenSecretName("default"), Namespace: "kape-system"},
		Data:       map[string][]byte{},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(server, empty).Build()

	if _, err := k8s.NewDaemonTokenStore(c).Ensure(context.Background(), server); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	var repaired corev1.Secret
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: k8s.DaemonTokenSecretName("default")}, &repaired); err != nil {
		t.Fatalf("reading Secret: %v", err)
	}
	if len(repaired.Data[k8s.DaemonTokenSecretKey]) != 64 {
		t.Fatal("empty token not repaired")
	}
}
