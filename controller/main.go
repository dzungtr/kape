// m1-controller → controller v1: talks gRPC directly to the deployed
// OpenShell gateway, creates sandboxes running the pi coding agent in RPC
// mode, and exposes a local HTTP/SSE agent API on localhost:3000 (8081 default).
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Config resolution: compiled defaults → KAPE_* env → flags (flags win).
	cfg, err := ConfigFromEnv()
	if err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	cfg.RegisterFlags(flag.CommandLine)
	flag.Parse()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	// Credential bindings (issue #264, ADR-0088): one log line names the
	// providers every sandbox policy will carry — provider names only, never
	// a host table's secrets.
	if len(cfg.CredentialBindings) > 0 {
		providers := make([]string, 0, len(cfg.CredentialBindings))
		for _, b := range cfg.CredentialBindings {
			providers = append(providers, b.Provider)
		}
		log.Printf("credential bindings: %d entries (%s)", len(cfg.CredentialBindings), strings.Join(providers, ", "))
	}

	// SLM monitor (issue #231): a missing or empty OpenRouter API key
	// disables the monitor entirely — single log line, everything else
	// unaffected (issue #230, story 11).
	if !cfg.Monitor.Enabled() {
		log.Printf("SLM monitor disabled: no OpenRouter API key configured (KAPE_MONITOR_OPENROUTER_API_KEY)")
	}

	tlsCfg, err := loadMtlsConfig(cfg.CredsDir)
	if err != nil {
		log.Fatalf("load mTLS creds from %s: %v", cfg.CredsDir, err)
	}
	conn, err := grpc.NewClient(cfg.GatewayAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithIdleTimeout(0),
	)
	if err != nil {
		log.Fatalf("grpc dial %s: %v", cfg.GatewayAddr, err)
	}
	gw := NewGateway(conn, cfg.ModelGatewayURL).WithDaemonToken(cfg.DaemonToken).
		WithDaemonCommand(cfg.DaemonCommandArgv()).
		WithCredentialBindings(cfg.CredentialBindings)

	// Durable event store (issue #195): one controller-local single-file log,
	// opened before any agent can publish; closed when the process exits.
	store, err := NewBoltEventStore(cfg.EventStorePath)
	if err != nil {
		log.Fatalf("open event store: %v", err)
	}
	defer store.Close()

	mgr := NewAgentManager(gw, cfg, store)
	if cfg.Monitor.Enabled() {
		// SLM monitor loop (issue #232): one timer per running agent,
		// assessment projection, terminal assessment in the settle path.
		mgr = mgr.WithMonitor(NewMonitor(NewSLMClient(cfg.Monitor, nil), cfg.Monitor.Interval))
	}
	if cfg.Transport == TransportPull {
		// Daemon addressing comes from the Sandbox CR (issue #214, D7): the
		// controller's first k8s API client (in-cluster SA, get/watch
		// sandboxes only — controller/deploy/rbac.yaml).
		res, err := NewCRDaemonAddressResolver(cfg.SandboxNamespace, cfg.DaemonPort)
		if err != nil {
			log.Fatalf("build sandbox CR address resolver: %v", err)
		}
		mgr = mgr.WithResolver(res)
	}
	if err := mgr.RebuildFromLog(context.Background()); err != nil {
		// Degrade, don't die: CR-derived views keep list/get working while
		// the log is unreadable (issue #197 semantics).
		log.Printf("registry replay degraded to CR-derived views: %v", err)
	}
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           NewRouter(mgr, cfg.ServerDescription),
		ReadHeaderTimeout: 30 * time.Second,
		// No overall timeouts: SSE streams are long-lived.
	}
	log.Printf("controller listening on %s (gateway %s, sandbox image %s, provider %s, model gateway %s)",
		cfg.ListenAddr, cfg.GatewayAddr, cfg.DefaultImage, cfg.DefaultProvider, cfg.ModelGatewayURL)
	log.Fatal(srv.ListenAndServe())
}

// loadMtlsConfig builds the TLS config for gateway mTLS auth.
func loadMtlsConfig(dir string) (*tls.Config, error) {
	caPEM, err := os.ReadFile(dir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read ca.crt: %w", err)
	}
	certPEM, err := os.ReadFile(dir + "/tls.crt")
	if err != nil {
		return nil, fmt.Errorf("read tls.crt: %w", err)
	}
	keyPEM, err := os.ReadFile(dir + "/tls.key")
	if err != nil {
		return nil, fmt.Errorf("read tls.key: %w", err)
	}
	return newTlsConfig(caPEM, certPEM, keyPEM)
}
