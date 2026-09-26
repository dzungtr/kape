// m1-controller: spike controller that talks gRPC directly to the deployed
// OpenShell gateway, creates sandboxes running the pi coding agent in RPC
// mode, and exposes a local HTTP/SSE API on localhost:8081.
package main

import (
	"flag"
	"fmt"
	"log"
	"crypto/tls"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Generous timeouts: first sandbox image pull may take ~1 minute.
	gwEndpoint := flag.String("gateway", "127.0.0.1:32353", "OpenShell gateway host:port")
	creds := flag.String("creds", os.Getenv("HOME")+"/.config/openshell/gateways/k8s/mtls", "mTLS creds dir (ca.crt/tls.crt/tls.key)")
	listen := flag.String("listen", ":8081", "HTTP listen address")
	providerName := flag.String("provider", "openrouter-spike", "OpenShell provider name attached to sandboxes")
	sandboxImage := flag.String("image", "ghcr.io/dzungtr/pi-openshell:openrouter", "sandbox OCI image")
	flag.Parse()

	tlsCfg, err := loadMtlsConfig(*creds)
	if err != nil {
		log.Fatalf("load mTLS creds from %s: %v", *creds, err)
	}
	conn, err := grpc.NewClient(*gwEndpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithIdleTimeout(0),
	)
	if err != nil {
		log.Fatalf("grpc dial %s: %v", *gwEndpoint, err)
	}
	gw := NewGateway(conn)

	mgr := NewSessionManager(gw, *providerName, *sandboxImage)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           NewRouter(mgr),
		ReadHeaderTimeout: 30 * time.Second,
		// No overall timeouts: SSE streams are long-lived.
	}
	log.Printf("m1-controller listening on %s (gateway %s, sandbox image %s)", *listen, *gwEndpoint, *sandboxImage)
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
