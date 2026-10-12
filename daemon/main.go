// daemon — the passive in-sandbox HTTP server (issue #210, ADR-0083 D2/D3).
// It owns the live pi RPC subprocess, spools unacked records on the sandbox
// PVC as `{spool_id, data}` envelopes, serves long-poll reads, and never dials
// the controller. The controller stays the seq authority; the spool's
// recover-from-file pattern keeps spool ids durable across daemon restarts.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config resolution: compiled defaults ← KAPE_* env ← flags (flags win), the
// controller main.go convention.
type Config struct {
	Addr         string
	AgentID      string
	Token        string
	SpoolPath    string
	SpoolMaxRecs int
	PiCommand    []string
}

// bootID is a fresh random id per daemon start (ADR-0083 D6): partitions keep
// it, deaths mint a new one.
func bootID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("boot id: %v", err)
	}
	return hex.EncodeToString(b)
}

// parseConfig builds the config from env and flags. envValue is consulted as
// each flag's default so a flag overrides env.
func parseConfig(args []string) Config {
	env := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	cfg := Config{
		Addr:         env("KAPE_DAEMON_ADDR", "127.0.0.1:9090"),
		AgentID:      env("KAPE_AGENT_ID", "unset"),
		Token:        env("KAPE_DAEMON_TOKEN", ""),
		SpoolPath:    env("KAPE_SPOOL_PATH", "/var/lib/kape-daemon/spool.jsonl"),
		SpoolMaxRecs: 10000,
	}
	piCmd := env("KAPE_PI_COMMAND", "")
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	fs.StringVar(&cfg.AgentID, "agent-id", cfg.AgentID, "agent id reported by /status")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "static bearer token placeholder (empty: all requests 401)")
	fs.StringVar(&cfg.SpoolPath, "spool-path", cfg.SpoolPath, "PVC spool file")
	fs.IntVar(&cfg.SpoolMaxRecs, "spool-max-records", cfg.SpoolMaxRecs, "spool cap before truncation")
	fs.StringVar(&piCmd, "pi-command", piCmd, "pi subprocess argv (comma-separated); empty = built-in fake pi")
	_ = fs.Parse(args)
	if piCmd != "" {
		for _, part := range strings.Split(piCmd, ",") {
			if part = strings.TrimSpace(part); part != "" {
				cfg.PiCommand = append(cfg.PiCommand, part)
			}
		}
	}
	return cfg
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "fakepi" {
		// Built-in scriptable fake pi: deterministic record emitter (issue #210).
		if err := runFakePi(os.Stdin, os.Stdout, os.Stderr); err != nil {
			log.Fatalf("fakepi: %v", err)
		}
		return
	}
	cfg := parseConfig(os.Args[1:])
	if err := run(cfg); err != nil {
		log.Fatalf("daemon: %v", err)
	}
}

func run(cfg Config) error {
	spool, err := NewSpool(cfg.SpoolPath, cfg.SpoolMaxRecs)
	if err != nil {
		return err
	}
	defer spool.Close()
	pi := NewPi(cfg.PiCommand, spool)
	if err := pi.Start(); err != nil {
		return fmt.Errorf("start pi: %w", err)
	}
	queue := NewCommandQueue()
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           NewRouter(cfg.AgentID, bootID(), spool, pi, queue, cfg.Token),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("daemon listening on %s (agent_id=%s spool=%s)", cfg.Addr, cfg.AgentID, cfg.SpoolPath)
	return srv.ListenAndServe()
}
