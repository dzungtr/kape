package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Daemon exposure (kape#274). The controller cannot dial the sandbox pod IP:
// OpenShell installs `openshell-sandbox-workloads`, whose only admitted ingress
// is the supervisor on :5500, so every non-supervisor packet to the workload
// pod is dropped (verified live: pod-to-pod curl of :9090 times out). The
// supported path is ExposeService: the gateway routes an HTTP endpoint through
// the supervisor's control plane to the daemon's LOOPBACK port inside the
// sandbox. The exposed authority (`<sandbox>.openshell.localhost:32353`) is a
// gateway routing name, not a cluster DNS name, so the transport below dials the
// gateway service and lets the URL host ride as Host/SNI — the gateway's server
// certificate carries `*.openshell.localhost`.
const (
	daemonExposureName = "" // unnamed endpoint: one per sandbox
	daemonScheme       = "https://"
)

// daemonExposureResolver resolves a sandbox to its exposed daemon URL. Expose
// is idempotent on the gateway, so a controller restart re-derives the address
// instead of holding state.
// daemonExposureGateway is the slice of the gateway the exposure resolver
// needs; a typed *Gateway is not required (tests use the fake).
type daemonExposureGateway interface {
	ExposeDaemon(ctx context.Context, sandboxName string) (string, error)
}

type daemonExposureResolver struct {
	gw daemonExposureGateway
}

// NewDaemonExposureResolver builds the pull-transport resolver.
func NewDaemonExposureResolver(gw daemonExposureGateway) DaemonAddressResolver {
	return &daemonExposureResolver{gw: gw}
}

// ResolveDaemonAddr returns https://<authority> for the sandbox, exposing the
// daemon endpoint on first use.
func (r *daemonExposureResolver) ResolveDaemonAddr(ctx context.Context, sandbox string) (string, error) {
	authority, err := r.gw.ExposeDaemon(ctx, sandbox)
	if err != nil {
		return "", err
	}
	return daemonScheme + authority, nil
}

// NewDaemonProxyTransport returns the transport every daemon request rides. All
// connections go to the gateway service; the request URL's host is preserved as
// Host (and therefore as TLS SNI), which is how the gateway picks the exposed
// endpoint.
func NewDaemonProxyTransport(tlsCfg *tls.Config, gatewayAddr string) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	cfg := tlsCfg.Clone()
	// The gateway terminates TLS with a certificate for *.openshell.localhost;
	// the URL host is the endpoint authority, so SNI/verification come from the
	// URL itself. No extra ServerName override.
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, gatewayAddr)
		},
		TLSClientConfig:     cfg,
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	return tr
}

// daemonAuthority extracts "host:port" from an exposure URL the gateway
// returned.
func daemonAuthority(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return "", errDaemonExposureURL(rawURL)
	}
	return u.Host, nil
}
