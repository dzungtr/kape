package k8s

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// DaemonTokenSecretKey is the data key holding the controller→daemon bearer
// token inside the operator-owned Secret.
const DaemonTokenSecretKey = "token"

// DaemonTokenStore generates and persists one daemon bearer token per
// KapeAgentServer (ADR-0083 D5). The Secret is owner-referenced, so it is
// garbage-collected with the CR, and it is never rendered into the CR status or
// the Deployment spec: only a SecretKeyRef name leaves this package.
type DaemonTokenStore struct {
	c client.Client
}

// NewDaemonTokenStore creates a DaemonTokenStore over the operator's client.
func NewDaemonTokenStore(c client.Client) *DaemonTokenStore {
	return &DaemonTokenStore{c: c}
}

// DaemonTokenSecretName is the Secret name for one KapeAgentServer.
func DaemonTokenSecretName(serverName string) string {
	return serverName + "-daemon-token"
}

// Ensure returns the Secret name, creating it with a fresh random token on
// first reconcile. An existing Secret is reused untouched (idempotent): the
// token is a long-lived bearer credential for already-running sandboxes, so
// regenerating it on every reconcile would strand them.
func (s *DaemonTokenStore) Ensure(ctx context.Context, server *v1alpha1.KapeAgentServer) (string, error) {
	name := DaemonTokenSecretName(server.Name)
	var existing corev1.Secret
	err := s.c.Get(ctx, types.NamespacedName{Namespace: server.Namespace, Name: name}, &existing)
	if err == nil {
		if _, ok := existing.Data[DaemonTokenSecretKey]; ok {
			return name, nil
		}
		// Present but empty: repair rather than fail — the sandbox would 401
		// every request with no way to see why.
		token, genErr := newDaemonToken()
		if genErr != nil {
			return "", genErr
		}
		existing.Data = map[string][]byte{DaemonTokenSecretKey: []byte(token)}
		if updErr := s.c.Update(ctx, &existing); updErr != nil {
			return "", fmt.Errorf("repairing daemon token Secret %s/%s: %w", server.Namespace, name, updErr)
		}
		return name, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("reading daemon token Secret %s/%s: %w", server.Namespace, name, err)
	}

	token, err := newDaemonToken()
	if err != nil {
		return "", err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: server.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "kape-operator",
				"kape.io/agentserver":          server.Name,
				"kape.io/daemon-token":         "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{DaemonTokenSecretKey: []byte(token)},
	}
	if err := controllerutil.SetControllerReference(server, secret, s.c.Scheme()); err != nil {
		return "", fmt.Errorf("owner-referencing daemon token Secret: %w", err)
	}
	if err := s.c.Create(ctx, secret); err != nil {
		// A concurrent reconcile won the create: reuse its token.
		if apierrors.IsAlreadyExists(err) {
			return name, nil
		}
		return "", fmt.Errorf("creating daemon token Secret %s/%s: %w", server.Namespace, name, err)
	}
	return name, nil
}

// newDaemonToken returns a 32-byte hex bearer token. The value is never logged
// or returned in an error.
func newDaemonToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating daemon token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
