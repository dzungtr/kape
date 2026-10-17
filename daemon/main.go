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
	"os/signal"
	"strings"
	"syscall"
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

	// Pi crash-ownership policy (spec #245 E4). Env names are a cross-slice
	// deliverable recorded in #245's Handoffs table.
	PiRapidExitWindow time.Duration // exit faster than this after start is rapid
	PiBackoffInitial  time.Duration // first rapid exit backs off this long
	PiBackoffMax      time.Duration // backoff cap
	PiStableRunReset  time.Duration // unexpected exit after a run ≥ this resets backoff
	PiShutdownGrace   time.Duration // SIGTERM→SIGKILL grace on daemon shutdown
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
		Addr:              env("KAPE_DAEMON_ADDR", "127.0.0.1:9090"),
		AgentID:           env("KAPE_AGENT_ID", "unset"),
		Token:             env("KAPE_DAEMON_TOKEN", ""),
		SpoolPath:         env("KAPE_SPOOL_PATH", "/var/lib/kape-daemon/spool.jsonl"),
		SpoolMaxRecs:      10000,
		PiRapidExitWindow: envDuration("KAPE_PI_RAPID_EXIT_WINDOW", "5s"),
		PiBackoffInitial:  envDuration("KAPE_PI_BACKOFF_INITIAL", "1s"),
		PiBackoffMax:      envDuration("KAPE_PI_BACKOFF_MAX", "30s"),
		PiStableRunReset:  envDuration("KAPE_PI_STABLE_RUN_RESET", "60s"),
		PiShutdownGrace:   envDuration("KAPE_PI_SHUTDOWN_GRACE", "5s"),
	}
	piCmd := env("KAPE_PI_COMMAND", "")
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	fs.StringVar(&cfg.AgentID, "agent-id", cfg.AgentID, "agent id reported by /status")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "static bearer token placeholder (empty: all requests 401)")
	fs.StringVar(&cfg.SpoolPath, "spool-path", cfg.SpoolPath, "PVC spool file")
	fs.IntVar(&cfg.SpoolMaxRecs, "spool-max-records", cfg.SpoolMaxRecs, "spool cap before truncation")
	dur := func(name, def, usage string, dst *time.Duration) {
		fs.Func(name, usage+" (duration; default "+def+")", func(s string) error {
			v, err := time.ParseDuration(s)
			if err != nil {
				return err
			}
			*dst = v
			return nil
		})
	}
	dur("pi-rapid-exit-window", "5s", "pi exit faster than this after start counts as rapid", &cfg.PiRapidExitWindow)
	dur("pi-backoff-initial", "1s", "backoff before the first rapid-exit respawn", &cfg.PiBackoffInitial)
	dur("pi-backoff-max", "30s", "respawn backoff cap", &cfg.PiBackoffMax)
	dur("pi-stable-run-reset", "60s", "unexpected exit after a run of at least this resets backoff", &cfg.PiStableRunReset)
	dur("pi-shutdown-grace", "5s", "SIGTERM→SIGKILL grace when killing pi on daemon shutdown", &cfg.PiShutdownGrace)
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

// envDuration resolves a KAPE_* duration env var (Go duration syntax),
// falling back to def when unset or unparseable (log the parse failure but
// keep booting on the default: the daemon must start).
func envDuration(key, def string) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		d, _ := time.ParseDuration(def)
		return d
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("%s=%q is not a duration, using default %s", key, v, def)
		d, _ = time.ParseDuration(def)
		return d
	}
	return d
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
	defer spool.Close() //nolint:errcheck
	pi := NewPi(cfg.PiCommand, spool)
	pi.rapidExitWindow = cfg.PiRapidExitWindow
	pi.backoffInitial = cfg.PiBackoffInitial
	pi.backoffMax = cfg.PiBackoffMax
	pi.stableRunReset = cfg.PiStableRunReset
	if piAutostart() {
		if err := pi.Start(); err != nil {
			return fmt.Errorf("start pi: %w", err)
		}
	}
	queue := NewCommandQueue()
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           NewRouter(cfg.AgentID, bootID(), spool, pi, queue, cfg.Token),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("daemon listening on %s (agent_id=%s spool=%s)", cfg.Addr, cfg.AgentID, cfg.SpoolPath)
	// Ordered shutdown (spec #245 E4): on SIGTERM/SIGINT pi is killed first
	// (SIGTERM→SIGKILL after the grace), then the daemon exits non-zero —
	// the sandbox exit_code stays the intentional-teardown signal (E2).
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, os.Interrupt)
	go func() {
		s := <-sigs
		log.Printf("daemon received %s; killing pi, then exiting", s)
		pi.Shutdown(cfg.PiShutdownGrace)
		os.Exit(1)
	}()
	return srv.ListenAndServe()
}

// piAutostart: pi autostart mirrors the real deployment (pi rides the
// sandbox session, not the daemon boot). The smoke image sets
// KAPE_PI_AUTOSTART=0 so a fresh boot reports pi_running:false — the D6
// daemon-death signature the failure matrix (issue #217) exercises; pi then
// spawns on the first prompt command via SendCommand.
func piAutostart() bool { return os.Getenv("KAPE_PI_AUTOSTART") != "0" }
