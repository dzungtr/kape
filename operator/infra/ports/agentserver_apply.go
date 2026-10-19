package ports

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	"github.com/kape-io/kape/operator/domain/agentserver"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// AgentServerApplyPort converges the objects rendered from a KapeAgentServer.
type AgentServerApplyPort interface {
	// Apply creates or patches the controller Deployment and the network
	// resources in the CR's namespace and removes rendered objects the CR no
	// longer declares. wiring is the operator-level cluster env appended to the
	// CR-derived container env (D5: plumbing, not intent).
	//
	// Returns an error wrapping ErrNotOwned when an object this CR claims to
	// own exists without the operator's ownership labels.
	Apply(ctx context.Context, server *v1alpha1.KapeAgentServer, desired agentserver.DesiredState, wiring domainconfig.ControllerWiring) error
	// DeploymentStatus reports whether the rendered controller Deployment is
	// available. found is false when the Deployment does not exist.
	DeploymentStatus(ctx context.Context, key types.NamespacedName) (available bool, found bool, err error)
}
