package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/kape-io/kape/kapeproxy/internal/proxy"
)

const (
	defaultConfigPath = "/etc/kapeproxy/config.yaml"
	defaultListenAddr = ":8080"
	shutdownTimeout   = 30 * time.Second
)

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	logger := log.With().Str("component", "kapeproxy").Logger()

	configPath := os.Getenv("KAPEPROXY_CONFIG_PATH")
	if configPath == "" {
		configPath = defaultConfigPath
	}

	cfg, err := proxy.LoadConfig(configPath)
	if err != nil {
		logger.Fatal().Err(err).Str("path", configPath).Msg("loading config")
	}

	rootCtx := context.Background()
	otelShutdown, err := proxy.InitTracer(rootCtx)
	if err != nil {
		logger.Warn().Err(err).Msg("OTEL init failed; tracing disabled")
		otelShutdown = func(context.Context) error { return nil }
	}

	// Build upstreams.
	upstreams := make(map[string]proxy.Upstream, len(cfg.Upstreams))
	for name, up := range cfg.Upstreams {
		upstreams[name] = proxy.NewMCPUpstream(rootCtx, name, up)
	}

	router := proxy.NewRouter(cfg, upstreams)
	redactor := proxy.NewRedactor()
	audit := proxy.NewAuditLogger(logger)
	server := proxy.NewServer(defaultListenAddr, router, redactor, audit, logger)

	// Run server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	// Wait for signal or server error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case err := <-errCh:
		if err != nil {
			logger.Error().Err(err).Msg("server error")
		}
	}

	shutdownCtx, cancel := context.WithTimeout(rootCtx, shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("graceful shutdown error")
	}
	if err := otelShutdown(shutdownCtx); err != nil {
		logger.Warn().Err(err).Msg("OTEL shutdown error")
	}
	logger.Info().Msg("kapeproxy stopped")
}
