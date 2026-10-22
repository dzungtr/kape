package k8s

import (
	"context"
	"fmt"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kape-io/kape/operator/domain/agentserver"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// SandboxReaderClusterRole is the cluster-scoped grant the chart installs for
// the controller's Sandbox-CR reads (agents.x-k8s.io, ADR-0083 D7). Every
// CR-rendered controller gets a same-namespace RoleBinding to it, so a
// KapeAgentServer in any namespace can read sandboxes while the cluster-scoped
// grant stays a chart-owned decision.
const SandboxReaderClusterRole = "kape-controller-sandbox-reader"

// AgentServerAdapter implements ports.AgentServerApplyPort with the
// controller-runtime client.
type AgentServerAdapter struct {
	client client.Client
}

// NewAgentServerAdapter creates a new AgentServerAdapter.
func NewAgentServerAdapter(c client.Client) *AgentServerAdapter {
	return &AgentServerAdapter{client: c}
}

// Apply converges the objects one KapeAgentServer renders: the controller
// ServiceAccount, its RoleBinding to the sandbox-reader ClusterRole, the
// controller Deployment, the Service, and the Ingress when the CR declares one.
// Objects the CR no longer declares are pruned; objects the operator did not
// render are never touched (ports.ErrNotOwned).
func (a *AgentServerAdapter) Apply(
	ctx context.Context,
	server *v1alpha1.KapeAgentServer,
	desired agentserver.DesiredState,
	wiring domainconfig.ControllerWiring,
) error {
	owned := []client.Object{
		buildControllerServiceAccount(server),
		buildControllerRoleBinding(server),
		buildControllerDeployment(server, desired.DeploymentEnv, wiring),
		desired.NetworkResources.Service,
	}
	if desired.NetworkResources.Ingress != nil {
		owned = append(owned, desired.NetworkResources.Ingress)
	}
	for _, obj := range owned {
		if err := a.ensure(ctx, server, obj); err != nil {
			return err
		}
	}
	if desired.NetworkResources.Ingress == nil {
		return a.prune(ctx, server, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{
			Name:      agentserver.ControllerName(server.Name),
			Namespace: server.Namespace,
		}})
	}
	return nil
}

// DeploymentStatus reports whether the controller Deployment is available:
// the controller has observed the current generation and has a ready replica.
func (a *AgentServerAdapter) DeploymentStatus(ctx context.Context, key types.NamespacedName) (bool, bool, error) {
	var dep appsv1.Deployment
	if err := a.client.Get(ctx, key, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("getting Deployment %s/%s: %w", key.Namespace, key.Name, err)
	}
	available := dep.Status.ObservedGeneration >= dep.Generation && dep.Status.AvailableReplicas > 0
	return available, true, nil
}

// ensure creates obj when absent and patches the operator-owned fields when
// present. It refuses to touch an object that lacks this CR's ownership labels.
func (a *AgentServerAdapter) ensure(ctx context.Context, server *v1alpha1.KapeAgentServer, desired client.Object) error {
	key := client.ObjectKeyFromObject(desired)
	kind := objectKind(desired)
	existing, err := newObjectLike(desired)
	if err != nil {
		return err
	}
	if err := a.client.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			if err := a.client.Create(ctx, desired); err != nil {
				return fmt.Errorf("creating %s %s/%s: %w", kind, key.Namespace, key.Name, err)
			}
			return nil
		}
		return fmt.Errorf("getting %s %s/%s: %w", kind, key.Namespace, key.Name, err)
	}
	if !ownedBy(existing, server.Name) {
		return fmt.Errorf("%w: %s %s/%s", ports.ErrNotOwned, kind, key.Namespace, key.Name)
	}

	patched := existing.DeepCopyObject().(client.Object)
	patched.SetLabels(desired.GetLabels())
	patched.SetAnnotations(desired.GetAnnotations())
	patched.SetOwnerReferences(desired.GetOwnerReferences())
	if err := applySpec(patched, desired); err != nil {
		return err
	}
	patch := client.MergeFrom(existing)
	delta, err := patch.Data(patched)
	if err != nil {
		return fmt.Errorf("diffing %s %s/%s: %w", kind, key.Namespace, key.Name, err)
	}
	if string(delta) == "{}" {
		return nil // already converged: leave resourceVersion untouched
	}
	if err := a.client.Patch(ctx, patched, patch); err != nil {
		return fmt.Errorf("patching %s %s/%s: %w", kind, key.Namespace, key.Name, err)
	}
	return nil
}

// prune deletes obj when it exists and this CR owns it. It is a no-op when the
// object is absent and refuses to delete an object the operator did not render.
func (a *AgentServerAdapter) prune(ctx context.Context, server *v1alpha1.KapeAgentServer, obj client.Object) error {
	key := client.ObjectKeyFromObject(obj)
	kind := objectKind(obj)
	existing, err := newObjectLike(obj)
	if err != nil {
		return err
	}
	if err := a.client.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting %s %s/%s: %w", kind, key.Namespace, key.Name, err)
	}
	if !ownedBy(existing, server.Name) {
		return nil // not ours to delete
	}
	if err := a.client.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting %s %s/%s: %w", kind, key.Namespace, key.Name, err)
	}
	return nil
}

// applySpec copies the fields the operator owns onto the live object. Fields
// the API server owns (Service clusterIP, Deployment status, …) are left alone.
func applySpec(patched, desired client.Object) error {
	switch d := desired.(type) {
	case *appsv1.Deployment:
		live := patched.(*appsv1.Deployment)
		inheritPodDefaults(&d.Spec.Template.Spec, &live.Spec.Template.Spec)
		// Replicas/strategy keep the live values for the same reason: the
		// renderer does not own them, so it must not zero them.
		if d.Spec.Strategy.Type == "" {
			d.Spec.Strategy = live.Spec.Strategy
		}
		live.Spec = d.Spec
	case *corev1.Service:
		live := patched.(*corev1.Service)
		live.Spec.Type = d.Spec.Type
		live.Spec.Selector = d.Spec.Selector
		live.Spec.Ports = d.Spec.Ports
	case *networkingv1.Ingress:
		live := patched.(*networkingv1.Ingress)
		if d.Spec.IngressClassName == nil {
			d.Spec.IngressClassName = live.Spec.IngressClassName
		}
		live.Spec = d.Spec
	case *corev1.ServiceAccount:
		// Ownership metadata is the whole desired state of a ServiceAccount.
	case *rbacv1.RoleBinding:
		live := patched.(*rbacv1.RoleBinding)
		live.RoleRef = d.RoleRef
		live.Subjects = d.Subjects
	default:
		return fmt.Errorf("unsupported object type %T", desired)
	}
	return nil
}

// inheritPodDefaults copies the API server's own defaulting into the rendered pod
// spec for every field the renderer does not set. Without it, `live.Spec =
// d.Spec` resets defaults to their zero values, the merge patch diff is never
// empty against a live object, and every reconcile bumps
// metadata.generation — leaving the controller Deployment permanently
// unavailable (observed as a hot loop, generation 1309 → 1511 in 20s).
func inheritPodDefaults(desired, live *corev1.PodSpec) {
	if desired.DNSPolicy == "" {
		desired.DNSPolicy = live.DNSPolicy
	}
	if desired.RestartPolicy == "" {
		desired.RestartPolicy = live.RestartPolicy
	}
	if desired.SchedulerName == "" {
		desired.SchedulerName = live.SchedulerName
	}
	if desired.TerminationGracePeriodSeconds == nil {
		desired.TerminationGracePeriodSeconds = live.TerminationGracePeriodSeconds
	}
	if desired.SecurityContext == nil {
		desired.SecurityContext = live.SecurityContext
	}
	if desired.ServiceAccountName == "" {
		desired.ServiceAccountName = live.ServiceAccountName
	}
	liveContainers := make(map[string]*corev1.Container, len(live.Containers))
	for i := range live.Containers {
		liveContainers[live.Containers[i].Name] = &live.Containers[i]
	}
	for i := range desired.Containers {
		inheritContainerDefaults(&desired.Containers[i], liveContainers[desired.Containers[i].Name])
	}
	liveVolumes := make(map[string]*corev1.Volume, len(live.Volumes))
	for i := range live.Volumes {
		liveVolumes[live.Volumes[i].Name] = &live.Volumes[i]
	}
	for i := range desired.Volumes {
		inheritVolumeDefaults(&desired.Volumes[i], liveVolumes[desired.Volumes[i].Name])
	}
}

func inheritContainerDefaults(desired, live *corev1.Container) {
	if live == nil {
		return
	}
	if desired.TerminationMessagePath == "" {
		desired.TerminationMessagePath = live.TerminationMessagePath
	}
	if desired.TerminationMessagePolicy == "" {
		desired.TerminationMessagePolicy = live.TerminationMessagePolicy
	}
	if desired.SecurityContext == nil {
		desired.SecurityContext = live.SecurityContext
	}
	inheritProbeDefaults(desired.ReadinessProbe, live.ReadinessProbe)
	inheritProbeDefaults(desired.LivenessProbe, live.LivenessProbe)
	inheritProbeDefaults(desired.StartupProbe, live.StartupProbe)
}

func inheritProbeDefaults(desired, live *corev1.Probe) {
	if desired == nil || live == nil {
		return
	}
	if desired.SuccessThreshold == 0 {
		desired.SuccessThreshold = live.SuccessThreshold
	}
	if desired.FailureThreshold == 0 {
		desired.FailureThreshold = live.FailureThreshold
	}
	if desired.TimeoutSeconds == 0 {
		desired.TimeoutSeconds = live.TimeoutSeconds
	}
	if desired.TerminationGracePeriodSeconds == nil {
		desired.TerminationGracePeriodSeconds = live.TerminationGracePeriodSeconds
	}
}

func inheritVolumeDefaults(desired, live *corev1.Volume) {
	if live == nil {
		return
	}
	if desired.Secret != nil && live.Secret != nil && desired.Secret.DefaultMode == nil {
		desired.Secret.DefaultMode = live.Secret.DefaultMode
	}
	if desired.ConfigMap != nil && live.ConfigMap != nil && desired.ConfigMap.DefaultMode == nil {
		desired.ConfigMap.DefaultMode = live.ConfigMap.DefaultMode
	}
	if desired.EmptyDir != nil && live.EmptyDir != nil && desired.EmptyDir.SizeLimit == nil {
		desired.EmptyDir.SizeLimit = live.EmptyDir.SizeLimit
	}
}

func ownedBy(obj client.Object, serverName string) bool {
	labels := obj.GetLabels()
	return labels[agentserver.AgentServerLabel] == serverName &&
		labels[agentserver.ManagedByLabel] == agentserver.ManagedByValue
}

func objectKind(obj client.Object) string {
	switch obj.(type) {
	case *appsv1.Deployment:
		return "Deployment"
	case *corev1.Service:
		return "Service"
	case *corev1.ServiceAccount:
		return "ServiceAccount"
	case *rbacv1.RoleBinding:
		return "RoleBinding"
	case *networkingv1.Ingress:
		return "Ingress"
	}
	return fmt.Sprintf("%T", obj)
}

// newObjectLike returns an empty object of the same kind, so a Get never merges
// the desired fields into the decoded response.
func newObjectLike(obj client.Object) (client.Object, error) {
	switch obj.(type) {
	case *appsv1.Deployment:
		return &appsv1.Deployment{}, nil
	case *corev1.Service:
		return &corev1.Service{}, nil
	case *corev1.ServiceAccount:
		return &corev1.ServiceAccount{}, nil
	case *rbacv1.RoleBinding:
		return &rbacv1.RoleBinding{}, nil
	case *networkingv1.Ingress:
		return &networkingv1.Ingress{}, nil
	}
	return nil, fmt.Errorf("unsupported object type %T", obj)
}

// controllerEnv merges the two env sources of a rendered controller, in order:
//
//  1. the CR-derived contract env (renderer output),
//  2. the fixed operator cluster wiring (D5: plumbing, not intent),
//  3. the chart-owned passthrough entries (ExtraEnv).
//
// A wiring or passthrough entry that would shadow a contract var is dropped —
// the KapeAgentServer stays the single source of truth for its own keys.
func controllerEnv(contractEnv []corev1.EnvVar, w domainconfig.ControllerWiring) []corev1.EnvVar {
	env := make([]corev1.EnvVar, 0, len(contractEnv)+5+len(w.ExtraEnv))
	seen := make(map[string]bool, len(contractEnv)+5+len(w.ExtraEnv))
	add := func(e corev1.EnvVar) {
		if seen[e.Name] {
			return
		}
		seen[e.Name] = true
		env = append(env, e)
	}
	for _, e := range contractEnv {
		add(e)
	}
	for _, e := range []corev1.EnvVar{
		{Name: "KAPE_GATEWAY_ADDR", Value: w.GatewayAddr},
		{Name: "KAPE_CREDS_DIR", Value: w.CredsDir},
		{Name: "KAPE_DEFAULT_PROVIDER", Value: w.DefaultProvider},
		{Name: "KAPE_TRANSPORT", Value: w.Transport},
		{Name: "KAPE_EVENT_STORE", Value: w.EventStore},
	} {
		add(e)
	}
	for _, e := range w.ExtraEnv {
		if domainconfig.IsContractEnv(e.Name) {
			continue // the CR contract is never shadowed by wiring
		}
		add(e)
	}
	return env
}

// controllerVolumes renders the wiring-driven mounts: the mTLS Secret at
// CredsDir, and an emptyDir for the durable event log's directory.
func controllerVolumes(w domainconfig.ControllerWiring) ([]corev1.Volume, []corev1.VolumeMount) {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	if w.CredsSecret != "" {
		volumes = append(volumes, corev1.Volume{
			Name:         "creds",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: w.CredsSecret}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "creds", MountPath: w.CredsDir, ReadOnly: true})
	}
	if dir := eventStoreDir(w.EventStore); dir != "" {
		volumes = append(volumes, corev1.Volume{
			Name:         "event-store",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "event-store", MountPath: dir})
	}
	return volumes, mounts
}

// eventStoreDir is the directory the event store file lives in. "" means the
// path is bare (the controller writes inside the container filesystem) or unset.
func eventStoreDir(eventStore string) string {
	if eventStore == "" {
		return ""
	}
	dir := path.Dir(eventStore)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

func buildControllerServiceAccount(server *v1alpha1.KapeAgentServer) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            agentserver.ControllerName(server.Name),
			Namespace:       server.Namespace,
			Labels:          agentserver.ObjectLabels(server.Name),
			OwnerReferences: []metav1.OwnerReference{agentserver.OwnerRef(server)},
		},
	}
}

func buildControllerRoleBinding(server *v1alpha1.KapeAgentServer) *rbacv1.RoleBinding {
	name := agentserver.ControllerName(server.Name)
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       server.Namespace,
			Labels:          agentserver.ObjectLabels(server.Name),
			OwnerReferences: []metav1.OwnerReference{agentserver.OwnerRef(server)},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     SandboxReaderClusterRole,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      name,
			Namespace: server.Namespace,
		}},
	}
}

func buildControllerDeployment(server *v1alpha1.KapeAgentServer, contractEnv []corev1.EnvVar, wiring domainconfig.ControllerWiring) *appsv1.Deployment {
	name := agentserver.ControllerName(server.Name)
	labels := agentserver.ObjectLabels(server.Name)
	podLabels := agentserver.SelectorLabels(server.Name)

	volumes, mounts := controllerVolumes(wiring)

	probe := func() *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("http")},
			},
			InitialDelaySeconds: 3,
			PeriodSeconds:       10,
		}
	}
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       server.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{agentserver.OwnerRef(server)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					ServiceAccountName: name,
					Containers: []corev1.Container{{
						Name:            "controller",
						Image:           server.Spec.Controller.Image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Ports: []corev1.ContainerPort{{
							Name:          "http",
							ContainerPort: agentserver.ControllerPort,
							Protocol:      corev1.ProtocolTCP,
						}},
						Env:            controllerEnv(contractEnv, wiring),
						EnvFrom:        wiring.ExtraEnvFrom,
						Resources:      controllerResources(server.Spec.Controller.Resources),
						VolumeMounts:   mounts,
						ReadinessProbe: probe(),
						LivenessProbe:  probe(),
					}},
					Volumes: volumes,
				},
			},
		},
	}
}

func controllerResources(override *corev1.ResourceRequirements) corev1.ResourceRequirements {
	if override != nil {
		return *override
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}
