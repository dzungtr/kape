package nats

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

// Connect establishes a NATS connection. If certFile, keyFile, and caFile are
// all non-empty, mTLS is configured using those paths. If any of the three
// values is empty, the connection is made without TLS and a warning is logged
// (local dev / CI fallback only — production deployments must set all three).
func Connect(url, name, certFile, keyFile, caFile string) (*natsgo.Conn, error) {
	opts := []natsgo.Option{
		natsgo.Name(name),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(2 * time.Second),
	}

	if certFile != "" && keyFile != "" && caFile != "" {
		tlsCfg, err := buildTLSConfig(certFile, keyFile, caFile)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		opts = append(opts, natsgo.Secure(tlsCfg))
		log.Info().
			Str("cert", certFile).
			Str("ca", caFile).
			Msg("mTLS enabled for NATS connection")
	} else {
		log.Warn().Msg("NATS_TLS_CERT / NATS_TLS_KEY / NATS_TLS_CA not set — connecting without mTLS (local dev only)")
	}

	return natsgo.Connect(url, opts...)
}

func buildTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading client key pair: %w", err)
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA cert %s: %w", caFile, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parsing CA cert from %s", caFile)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
