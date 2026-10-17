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
	"time"
)

// Pi is the live RPC subprocess inside the sandbox (ADR-0083 D3): the daemon
// owns it, spools its stdout verbatim, turns its stderr into pi_stderr
// notices, and its exit into a pi_exit notice. The stub's default pi is the
// built-in fake pi (this binary run with argv[1] = "fakepi"), a scriptable
// emitter of deterministic records; Epic 3's pi_restart notice is reserved.
type Pi struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	running    atomic.Bool
	notices    *Spool
	command    []string // pi command line; empty means self-fakepi
	sessionID  string   // session-pinned --session-id (spec #245 E3); empty = no flags
	sessionDir string   // pi --session-dir (PVC session tree)
}

// NewPi wires the pi subprocess manager; command is the argv to run (nil = the
// built-in fake pi: this binary invoked with the fakepi subcommand). The
// session-pinned pi session id (spec #245 E3, issue #249) and its PVC
// session dir are appended to the launch argv in Start, so the built-in fake
// pi (self-resolved there) stays argv-compatible and an empty session id
// leaves the argv unchanged (local runs).
func NewPi(command []string, notices *Spool, sessionID, sessionDir string) *Pi {
	return &Pi{notices: notices, command: command, sessionID: sessionID, sessionDir: sessionDir}
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
	p.cmd, p.stdin = cmd, stdin
	p.running.Store(true)
	go p.relayStdout(stdout)
	go p.relayStderr(stderr)
	go p.awaitExit(cmd)
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
	p.mu.Unlock()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
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
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// Running reports pi liveness for GET /status.
func (p *Pi) Running() bool { return p.running.Load() }

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
