// m1-controller → controller v1: talks gRPC directly to the deployed
// OpenShell gateway, creates sandboxes running the pi coding agent in RPC
// mode, and exposes a local HTTP/SSE agent API on localhost:3000 (8081 default).
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Config resolution: compiled defaults → KAPE_* env → flags (flags win).
	cfg := ConfigFromEnv()
	cfg.RegisterFlags(flag.CommandLine)
	flag.Parse()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
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
	gw := NewGateway(conn, cfg.ModelGatewayURL)

	mgr := NewAgentManager(gw, cfg)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           NewRouter(mgr),
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
