package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// ServiceAccountAdapter implements ports.ServiceAccountPort.
type ServiceAccountAdapter struct {
	client client.Client
}

// NewServiceAccountAdapter creates a new ServiceAccountAdapter.
func NewServiceAccountAdapter(c client.Client) *ServiceAccountAdapter {
	return &ServiceAccountAdapter{client: c}
}

// serviceAccountName returns the conventional ServiceAccount name for a handler.
func serviceAccountName(handlerName string) string {
	return "kape-handler-" + handlerName
}

// Ensure creates the handler ServiceAccount if it does not exist. Idempotent.
func (a *ServiceAccountAdapter) Ensure(ctx context.Context, handler *v1alpha1.KapeHandler) error {
	name := serviceAccountName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}

	var sa corev1.ServiceAccount
	err := a.client.Get(ctx, key, &sa)
	if err == nil {
		return nil // already exists
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting ServiceAccount %s/%s: %w", handler.Namespace, name, err)
	}

	sa = buildServiceAccount(handler)
	if err := a.client.Create(ctx, &sa); err != nil {
		return fmt.Errorf("creating ServiceAccount %s/%s: %w", handler.Namespace, name, err)
	}
	return nil
}

func buildServiceAccount(handler *v1alpha1.KapeHandler) corev1.ServiceAccount {
	sa := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName(handler.Name),
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
			},
		},
		// No automountServiceAccountToken here — set false on the pod spec instead.
	}
	setOwnerRef(handler, &sa.ObjectMeta)
	return sa
}
