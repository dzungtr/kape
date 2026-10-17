package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Pi is the live RPC subprocess inside the sandbox (ADR-0083 D3): the daemon
// owns it, spools its stdout verbatim, turns its stderr into pi_stderr
// notices, and its exit into a pi_exit notice. The stub's default pi is the
// built-in fake pi (this binary run with argv[1] = "fakepi"), a scriptable
// emitter of deterministic records.
//
// Crash ownership (spec #245 E4): pi is respawned lazily — on the next
// command, never auto-reprompted — with a pi_restart notice on every
// respawn-after-unexpected-exit (exit code + backoff state). Exit <5 s after
// start is a rapid exit and backs the next respawn off exponentially
// (1 s→30 s cap); a stable run ≥60 s resets the backoff. The daemon never
// gives up permanently.
type Pi struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	running    atomic.Bool
	notices    *Spool
	command    []string // pi command line; empty means self-fakepi
	sessionID  string   // session-pinned --session-id (spec #245 E3); empty = no flags
	sessionDir string   // pi --session-dir (PVC session tree)

	// Crash-ownership state (all guarded by mu unless noted).
	generation         int           // process generations started; 0 before the first
	lastStart          time.Time     // start time of the live/latest generation
	lastExitCode       int           // exit code of the latest generation
	lastExitUnexpected bool          // latest exit was not daemon-initiated (Kill/Shutdown)
	intentional        bool          // set by Kill/Shutdown; consumed by the next Start
	restarts           int           // respawn count (Starts that launched after a previous generation)
	backoff            time.Duration // current respawn backoff (0 = none)
	rapidStreak        int           // consecutive rapid (<rapidExitWindow) unexpected exits

	// Backoff policy (spec #245 E4); overridable for tests.
	rapidExitWindow time.Duration       // exit faster than this after start is rapid (default 5s)
	backoffInitial  time.Duration       // first rapid exit backs off this long (default 1s)
	backoffMax      time.Duration       // backoff cap (default 30s)
	stableRunReset  time.Duration       // an unexpected exit after a run ≥ this resets backoff (default 60s)
	now             func() time.Time    // injectable clock
	sleep           func(time.Duration) // injectable sleeper
}

// backoffDefaults mirrors the compiled defaults resolved from env in main.go
// (KAPE_PI_* — see parseConfig).
const (
	defaultRapidExitWindow = 5 * time.Second
	defaultBackoffInitial  = time.Second
	defaultBackoffMax      = 30 * time.Second
	defaultStableRunReset  = time.Minute
)

// NewPi wires the pi subprocess manager; command is the argv to run (nil = the
// built-in fake pi: this binary invoked with the fakepi subcommand). The
// session-pinned pi session id (spec #245 E3, issue #249) and its PVC
// session dir are appended to the launch argv in Start, so the built-in fake
// pi (self-resolved there) stays argv-compatible and an empty session id
// leaves the argv unchanged (local runs). Backoff policy defaults are the
// compiled E4 defaults; main.go overrides them from KAPE_PI_* env.
func NewPi(command []string, notices *Spool, sessionID, sessionDir string) *Pi {
	return &Pi{
		notices:         notices,
		command:         command,
		sessionID:       sessionID,
		sessionDir:      sessionDir,
		rapidExitWindow: defaultRapidExitWindow,
		backoffInitial:  defaultBackoffInitial,
		backoffMax:      defaultBackoffMax,
		stableRunReset:  defaultStableRunReset,
		now:             time.Now,
		sleep:           time.Sleep,
	}
}

// piArgv resolves the launch argv for a configured command: the command with
// the session-pinned resume flags appended (spec #245 E3, issue #249), or nil
// when no command is configured — Start then resolves the built-in fake pi
// with the same flags.
func piArgv(command []string, sessionID, sessionDir string) []string {
	if len(command) == 0 {
		return nil // built-in fake pi; Start appends the flags to self+fakepi
	}
	return append(append([]string{}, command...), piFlags(sessionID, sessionDir)...)
}

// piFlags renders the pi resume flags for a session-pinned session id; the
// dir defaults to the sandbox PVC path when unset alongside a pinned id.
func piFlags(sessionID, sessionDir string) []string {
	if sessionID == "" {
		return nil
	}
	if sessionDir == "" {
		sessionDir = "/sandbox/kape/sessions"
	}
	return []string{"--session-dir", sessionDir, "--session-id", sessionID}
}

// Start launches pi and its stdout/stderr relay pumps. pi_running flips true.
func (p *Pi) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running.Load() {
		return nil
	}
	argv := piArgv(p.command, p.sessionID, p.sessionDir)
	if len(argv) == 0 {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve self for fake pi: %w", err)
		}
		argv = append([]string{self, "fakepi"}, piFlags(p.sessionID, p.sessionDir)...)
	}
	// Lazy-respawn backoff (E4): when the previous generation exited
	// unexpectedly, hold the respawn for the current backoff before
	// launching. The sleep happens outside the lock and a concurrent starter
	// may win the race; the loser then observes running and returns.
	if delay := p.backoff; p.lastExitUnexpected && delay > 0 {
		p.mu.Unlock()
		p.sleep(delay)
		p.mu.Lock()
		if p.running.Load() {
			return nil
		}
	}
	// Snapshot the respawn decision before clearing the exit state: the
	// pi_restart notice describes the generation being replaced.
	respawnAfterUnexpected := p.lastExitUnexpected
	p.lastExitUnexpected = false
	p.intentional = false
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // configured pi command, daemon-side trust boundary
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("pi stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pi stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("pi stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pi: %w", err)
	}
	if p.generation > 0 {
		p.restarts++
	}
	p.generation++
	p.lastStart = p.now()
	p.cmd, p.stdin = cmd, stdin
	p.running.Store(true)
	go p.relayStdout(stdout)
	go p.relayStderr(stderr)
	go p.awaitExit(cmd)
	if wasRespawn := p.generation > 1; wasRespawn && respawnAfterUnexpected {
		n, _ := json.Marshal(map[string]any{
			"type":       "pi_restart",
			"exit_code":  p.lastExitCode,
			"backoff_ms": p.backoff.Milliseconds(),
			"restarts":   p.restarts,
		})
		if _, err := p.notices.Append(string(n)); err != nil {
			log.Printf("spool append failed: %v", err)
		}
	}
	return nil
}

// relayStdout spools each pi stdout line verbatim — never re-wrapped. Malformed
// lines are still spooled as raw text inside a JSON string (serving-time
// problem, not transport). A stdout line larger than the scanner's 1MiB token
// limit does not kill the relay: the oversize line is reported as a pi_stderr
// notice and drained unbounded via bufio.Reader, then normal scanning resumes.
func (p *Pi) relayStdout(r io.Reader) {
	// bufio.Reader (not bufio.Scanner): Scanner's fixed token cap makes a
	// >1MiB stdout line silently kill the relay. ReadString is unbounded, so
	// oversized lines flow through and stdout keeps streaming until pi dies.
	rd := bufio.NewReader(r)
	for {
		line, err := rd.ReadString('\n')
		p.spoolStdoutLine(strings.TrimRight(line, "\n"))
		if err != nil {
			if err != io.EOF {
				p.stderrNotice(fmt.Sprintf("pi stdout relay error: %v", err))
			}
			return
		}
	}
}

// spoolStdoutLine spools one verbatim stdout line (see relayStdout).
func (p *Pi) spoolStdoutLine(line string) {
	if line == "" {
		return
	}
	// Verbatim: a well-formed JSON record is spooled as-is, never
	// re-wrapped. Malformed output falls back to a JSON string so the
	// envelope stays parseable (honest history, serving-time problem).
	data := line
	if !json.Valid([]byte(line)) {
		if raw, err := json.Marshal(line); err == nil {
			data = string(raw)
		}
	}
	if _, err := p.notices.Append(data); err != nil {
		log.Printf("spool append failed: %v", err)
	}
}

// relayStderr turns each pi stderr line into a pi_stderr notice record.
func (p *Pi) relayStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		p.stderrNotice(sc.Text())
	}
}

// stderrNotice spools text as a pi_stderr notice record.
func (p *Pi) stderrNotice(text string) {
	if text == "" {
		return
	}
	n, _ := json.Marshal(map[string]string{"type": "pi_stderr", "text": text})
	if _, err := p.notices.Append(string(n)); err != nil {
		log.Printf("spool append failed: %v", err)
	}
}

// Shutdown orders the daemon-side teardown (E4): pi is asked to exit with
// SIGTERM first and only SIGKILLed once the grace expires. Intentional exit:
// the subsequent exit does not count as unexpected for the respawn path.
// Death is observed via pi_running (awaitExit owns cmd.Wait — double Wait on
// the same Cmd is not allowed).
func (p *Pi) Shutdown(grace time.Duration) {
	p.mu.Lock()
	cmd := p.cmd
	p.intentional = true
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck
	deadline := time.Now().Add(grace)
	for p.Running() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if p.Running() {
		_ = cmd.Process.Kill() //nolint:errcheck
	}
	deadline = time.Now().Add(time.Second)
	for p.Running() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
}

// awaitExit emits a pi_exit notice when the process ends and flips pi_running.
// The Store is guarded: a stale waiter (an older generation's cmd that was
// already dead when SendCommand respawned) must not clear pi_running for the
// live generation — that would wedge ADR-0083 D6's reconnect preflight on a
// permanently false pi_running.
func (p *Pi) awaitExit(cmd *exec.Cmd) {
	err := cmd.Wait()
	p.mu.Lock()
	if p.cmd == cmd {
		p.running.Store(false)
	}
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	p.lastExitCode = code
	// Unexpected exit: anything the daemon did not initiate (Kill/Shutdown).
	// Backoff accounting (E4): a rapid exit (< rapidExitWindow after start)
	// extends the exponential backoff (initial << streak, capped); a run of
	// at least stableRunReset proves stability and resets the guard entirely.
	// Runs in between neither extend nor reset the guard.
	if !p.intentional {
		p.lastExitUnexpected = true
		uptime := p.now().Sub(p.lastStart)
		switch {
		case uptime < p.rapidExitWindow:
			p.rapidStreak++
			d := p.backoffInitial << (p.rapidStreak - 1)
			if d > p.backoffMax || d <= 0 { // cap, and guard shift overflow
				d = p.backoffMax
			}
			p.backoff = d
		case uptime >= p.stableRunReset:
			p.rapidStreak = 0
			p.backoff = 0
		}
	}
	p.mu.Unlock()
	n, _ := json.Marshal(map[string]any{"type": "pi_exit", "exit_code": code})
	if _, err := p.notices.Append(string(n)); err != nil {
		log.Printf("spool append failed: %v", err)
	}
}

// SendCommand writes a command to pi's stdin as one JSON line. A prompt on a
// dead pi respawns it (silent respawn in the stub — the pi_restart notice is
// reserved for Epic 3).
func (p *Pi) SendCommand(ctx context.Context, c Command) error {
	if c.Type == CommandAbort {
		p.Kill()
		return nil
	}
	if err := p.Start(); err != nil {
		return err
	}
	line, err := json.Marshal(map[string]any{
		"type":       c.Type,
		"command_id": c.ID,
		"payload":    c.Payload,
	})
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdin == nil {
		return fmt.Errorf("pi stdin unavailable")
	}
	_, err = p.stdin.Write(append(line, '\n'))
	return err
}

// Kill aborts pi: SIGTERM then SIGKILL; the exit surfaces as pi_exit.
func (p *Pi) Kill() {
	p.mu.Lock()
	cmd := p.cmd
	p.intentional = true
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// Running reports pi liveness for GET /status.
func (p *Pi) Running() bool { return p.running.Load() }

// Restarts reports the respawn count for GET /status: every pi process start
// after the daemon's first generation of this boot.
func (p *Pi) Restarts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restarts
}

// Backoff reports the current respawn backoff for GET /status (0 = none).
func (p *Pi) Backoff() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backoff
}

// fakePiRecord is the deterministic record vocabulary fake pi emits per prompt
// turn: turn_started → tool_call → text_delta → agent_settled (issue #210).
type fakePiRecord struct {
	Type   string         `json:"type"`
	TurnID string         `json:"turn_id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Text   string         `json:"text,omitempty"`
	Args   map[string]any `json:"arguments,omitempty"`
}

// runFakePi is the built-in scriptable emitter (this binary with argv[1]
// "fakepi"): reads one JSON command per stdin line and, for each prompt, emits
// the deterministic four-record turn on stdout. Steer/abort/clear_queue are
// accepted and no-op in the stub. Determinism: turn ids are ordinal counters.
func runFakePi(stdin io.Reader, stdout, stderr io.Writer) error {
	out := bufio.NewWriter(stdout)
	defer out.Flush() //nolint:errcheck
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	turn := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var c struct {
			Type    string `json:"type"`
			Payload struct {
				Message string `json:"message"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			fmt.Fprintf(stderr, "fakepi: bad command line: %v\n", err) //nolint:errcheck
			continue
		}
		if c.Type != CommandPrompt {
			continue
		}
		turn++
		turnID := fmt.Sprintf("t-%04d", turn)
		// hold_ms: mid-turn pacing for the failure matrix (issue #217) — the
		// first half of the turn emits, then the turn pauses before settling,
		// so a mid-turn outage sees records still pending (un-spooled or
		// un-acked) instead of a turn that settles in milliseconds.
		// The controller sends the prompt payload verbatim ({message}), so
		// the smoke drives pacing via a JSON-encoded message body.
		var msg struct {
			HoldMs int `json:"hold_ms"`
		}
		_ = json.Unmarshal([]byte(c.Payload.Message), &msg)
		hold := time.Duration(msg.HoldMs) * time.Millisecond
		recs := []fakePiRecord{
			{Type: "turn_started", TurnID: turnID},
			{Type: "tool_call", TurnID: turnID, Name: "read_file", Args: map[string]any{"path": "README.md"}},
		}
		for _, rec := range recs {
			b, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			out.Write(b)        //nolint:errcheck
			out.WriteByte('\n') //nolint:errcheck
			out.Flush()         //nolint:errcheck
		}
		if hold > 0 {
			time.Sleep(hold)
		}
		recs = []fakePiRecord{
			{Type: "text_delta", TurnID: turnID, Text: "Working on it."},
			{Type: "agent_settled", TurnID: turnID},
		}
		for _, rec := range recs {
			b, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			out.Write(b)        //nolint:errcheck
			out.WriteByte('\n') //nolint:errcheck
			out.Flush()         //nolint:errcheck
		}
	}
	return sc.Err()
}
