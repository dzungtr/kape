package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"k8s.io/client-go/rest"
)

// resolverForCR returns a CRDaemonAddressResolver whose REST client runs
// against an in-process fake apiserver (the sandboxed test host cannot dial
// loopback; the wire path is exercised live by the #217 smoke lane).
func resolverForCR(t *testing.T, body string) *CRDaemonAddressResolver {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/agents.x-k8s.io/v1beta1/namespaces/openshell/sandboxes/sbx-x" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	rc, err := restClientFor(&rest.Config{Host: "http://fake-apiserver.test", Transport: handlerRT{h}})
	if err != nil {
		t.Fatalf("restClientFor: %v", err)
	}
	return NewCRDaemonAddressResolverForClient(rc, "openshell", "9090")
}

// --- AC: puller resolves the daemon address from the Sandbox CR ---

func TestResolveDaemonAddrPodIPsPrimary(t *testing.T) {
	r := resolverForCR(t, `{
		"apiVersion":"agents.x-k8s.io/v1beta1","kind":"Sandbox",
		"spec":{"service":true},
		"status":{"podIPs":["10.244.0.7","fd00::7"],"serviceFQDN":"sbx-x.openshell.svc.cluster.local"}
	}`)
	base, err := r.ResolveDaemonAddr(context.Background(), "sbx-x")
	if err != nil {
		t.Fatalf("ResolveDaemonAddr: %v", err)
	}
	// Primary: podIPs[0] — the direct pod path bypasses the router (D7).
	if base != "http://10.244.0.7:9090" {
		t.Fatalf("base = %q, want http://10.244.0.7:9090", base)
	}
}

func TestResolveDaemonAddrServiceFQDNFallback(t *testing.T) {
	// spec.service: true (headless Service exists) and no podIPs: the
	// fallback is the headless-Service DNS name.
	r := resolverForCR(t, `{
		"apiVersion":"agents.x-k8s.io/v1beta1","kind":"Sandbox",
		"spec":{"service":true},
		"status":{"podIPs":[]}
	}`)
	base, err := r.ResolveDaemonAddr(context.Background(), "sbx-x")
	if err != nil {
		t.Fatalf("ResolveDaemonAddr: %v", err)
	}
	if base != "http://sbx-x.openshell.svc.cluster.local:9090" {
		t.Fatalf("base = %q, want the headless-Service FQDN", base)
	}
}

func TestResolveDaemonAddrPrefersReportedServiceFQDN(t *testing.T) {
	// Upstream reports status.serviceFQDN (honors a custom cluster domain);
	// the fallback prefers it over the constructed {name}.{ns}.svc.cluster.local.
	r := resolverForCR(t, `{
		"apiVersion":"agents.x-k8s.io/v1beta1","kind":"Sandbox",
		"spec":{"service":true},
		"status":{"podIPs":null,"serviceFQDN":"sbx-x.openshell.svc.mydomain.internal"}
	}`)
	base, err := r.ResolveDaemonAddr(context.Background(), "sbx-x")
	if err != nil {
		t.Fatalf("ResolveDaemonAddr: %v", err)
	}
	if base != "http://sbx-x.openshell.svc.mydomain.internal:9090" {
		t.Fatalf("base = %q, want the reported serviceFQDN", base)
	}
}

func TestResolveDaemonAddrNoServiceNoIPsFails(t *testing.T) {
	r := resolverForCR(t, `{
		"apiVersion":"agents.x-k8s.io/v1beta1","kind":"Sandbox",
		"spec":{"service":false},
		"status":{"podIPs":[]}
	}`)
	if _, err := r.ResolveDaemonAddr(context.Background(), "sbx-x"); err == nil {
		t.Fatal("ResolveDaemonAddr with empty podIPs and spec.service disabled: err = nil, want error")
	}
}

func TestResolveDaemonAddrMissingCRFails(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	rc, err := restClientFor(&rest.Config{Host: "http://fake-apiserver.test", Transport: handlerRT{h}})
	if err != nil {
		t.Fatalf("restClientFor: %v", err)
	}
	r := NewCRDaemonAddressResolverForClient(rc, "openshell", "9090")
	if _, err := r.ResolveDaemonAddr(context.Background(), "sbx-gone"); err == nil {
		t.Fatal("ResolveDaemonAddr for missing CR: err = nil, want error")
	}
}

// --- AC: re-resolve from the CR on EVERY reconnect (podIP churn) ---

// fakeResolver is a scripted DaemonAddressResolver recording every call.
type fakeResolver struct {
	mu       sync.Mutex
	addr     string
	resolved []string
}

func (f *fakeResolver) SetAddr(addr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addr = addr
}

func (f *fakeResolver) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resolved...)
}

func (f *fakeResolver) ResolveDaemonAddr(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, f.addr)
	return f.addr, nil
}

func TestPullerReResolvesDaemonAddressOnReconnect(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))

	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	// Resolution seam: the puller consumes a resolved address; the fake
	// resolver hands out changing addresses (a pod restart churns podIPs).
	res := &fakeResolver{addr: "http://pod-1:9090"}
	mgr.resolver = res
	// In-process transport: the sandboxed test host cannot dial loopback.
	mgr.daemonTransport = handlerRT{d.handler()}

	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	d.Append(`{"type":"turn_started","turn_id":"t-0"}`)
	waitFor(t, func() bool { return agent.hub.Origin() == 1 }, "first address resolves and pulls")

	// Pod restart: the daemon now answers at a new podIP. Pull failures must
	// drop the cached client and re-read the CR before the next pull.
	d.FailNext(3)
	res.SetAddr("http://pod-2:9090")
	// The next three pulls fail fast (no records race: the spool is empty
	// until after the reconnects), so the puller must re-resolve each time.
	waitFor(t, func() bool { return len(res.Calls()) >= 2 }, "re-resolve after pull failure")

	calls := res.Calls()
	if calls[1] != "http://pod-2:9090" {
		t.Fatalf("post-failure re-resolve used %q, want the new address http://pod-2:9090 (calls=%v)", calls[1], calls)
	}
	// Recovery: the re-resolved client drains the spool and settles the turn.
	d.Append(`{"type":"agent_settled","turn_id":"t-0"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "re-resolved address pulls after reconnect")
}
