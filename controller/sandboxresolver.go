package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

// Daemon address resolution (issue #214, ADR-0083 D7): the puller dials the
// in-sandbox daemon at a fixed port, discovered from the agent-sandbox
// Sandbox CR — status.podIPs (primary) or the headless-Service DNS name
// (fallback, requires spec.service: true). Resolution is its own seam: the
// puller consumes a resolved address and re-resolves from the CR on every
// reconnect (podIPs churn when the pod restarts).
//
// The Sandbox CR is `agents.x-k8s.io/v1beta1` (verified against upstream
// agent-sandbox CRDs): status.podIPs []string, status.serviceFQDN string,
// spec.service *bool. This is the controller's first k8s API usage — the
// in-cluster ServiceAccount only needs get/watch on sandboxes in the sandbox
// namespace (controller/deploy/rbac.yaml).

// DaemonAddressResolver resolves one agent's sandbox to a daemon base URL
// (http://host:port). Implementations must be safe for concurrent use.
type DaemonAddressResolver interface {
	// ResolveDaemonAddr returns the daemon base URL for the named sandbox.
	ResolveDaemonAddr(ctx context.Context, sandbox string) (string, error)
}

// sandboxGV is the agent-sandbox Sandbox CR group-version.
var sandboxGV = schema.GroupVersion{Group: "agents.x-k8s.io", Version: "v1beta1"}

// CRDaemonAddressResolver reads Sandbox CRs through a Kubernetes REST client
// and resolves the daemon address: status.podIPs[0] (primary — the direct
// pod path "bypasses the router") → the headless-Service DNS name
// {sandbox}.{ns}.svc.cluster.local (fallback; upstream reports it as
// status.serviceFQDN, which we prefer to honor a custom cluster domain) →
// error when neither is available.
type CRDaemonAddressResolver struct {
	rc   rest.Interface
	ns   string
	port string
}

// NewCRDaemonAddressResolver builds the resolver from the in-cluster
// ServiceAccount (rest.InClusterConfig). Requests carry cfg.Timeout so a
// wedged kube-apiserver cannot stall the puller beyond its backoff.
func NewCRDaemonAddressResolver(ns, port string) (*CRDaemonAddressResolver, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster k8s config (the controller must run in-cluster with a ServiceAccount permitted get/watch on sandboxes): %w", err)
	}
	cfg.Timeout = 10 * time.Second
	rc, err := restClientFor(cfg)
	if err != nil {
		return nil, err
	}
	return &CRDaemonAddressResolver{rc: rc, ns: ns, port: port}, nil
}

// restClientFor builds an unstructured REST client for the Sandbox
// group-version (the controller ships no typed agent-sandbox types).
func restClientFor(cfg *rest.Config) (rest.Interface, error) {
	c := *cfg
	c.GroupVersion = &sandboxGV
	c.NegotiatedSerializer = serializer.NewCodecFactory(runtime.NewScheme()).WithoutConversion()
	return rest.RESTClientFor(&c)
}

// NewCRDaemonAddressResolverForClient assembles the resolver over a
// caller-supplied REST client (tests point it at a fake apiserver).
func NewCRDaemonAddressResolverForClient(rc rest.Interface, ns, port string) *CRDaemonAddressResolver {
	return &CRDaemonAddressResolver{rc: rc, ns: ns, port: port}
}

// sandboxCR is the slice of the agent-sandbox Sandbox CR the resolver reads.
type sandboxCR struct {
	Spec struct {
		Service *bool `json:"service"`
	} `json:"spec"`
	Status struct {
		PodIPs      []string `json:"podIPs"`
		ServiceFQDN string   `json:"serviceFQDN"`
	} `json:"status"`
}

// ResolveDaemonAddr resolves the daemon base URL for sandbox.
func (r *CRDaemonAddressResolver) ResolveDaemonAddr(ctx context.Context, sandbox string) (string, error) {
	raw, err := r.rc.Get().
		AbsPath("/apis/agents.x-k8s.io/v1beta1/namespaces/" + r.ns + "/sandboxes/" + sandbox).
		Do(ctx).Raw()
	if err != nil {
		return "", fmt.Errorf("get Sandbox %s/%s: %w", r.ns, sandbox, err)
	}
	var sb sandboxCR
	if err := json.Unmarshal(raw, &sb); err != nil {
		return "", fmt.Errorf("decode Sandbox %s/%s: %w", r.ns, sandbox, err)
	}
	if len(sb.Status.PodIPs) > 0 {
		return "http://" + sb.Status.PodIPs[0] + ":" + r.port, nil
	}
	if sb.Spec.Service != nil && *sb.Spec.Service {
		fqdn := sb.Status.ServiceFQDN
		if fqdn == "" {
			// Upstream defaults the headless Service name to the Sandbox name.
			fqdn = sandbox + "." + r.ns + ".svc.cluster.local"
		}
		return "http://" + fqdn + ":" + r.port, nil
	}
	return "", fmt.Errorf("sandbox %s/%s exposes no daemon address (status.podIPs empty and spec.service not enabled)", r.ns, sandbox)
}
