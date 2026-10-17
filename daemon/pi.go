package main

import (
	"bufio"
	"bytes"
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
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	running atomic.Bool
	notices *Spool
	command []string // pi command line; empty means self-fakepi

	// piQueueMu guards the last-known pi queue state, tracked from the
	// queue_update records relayed on stdout (issue #250 / spec #245 E6):
	// the abort front door must spool the drained texts as honest history.
	// The queue is pi-runtime-only (issue #246 findings): it resets on exit.
	piQueueMu      sync.Mutex
	queuedSteering []string
	queuedFollowUp []string
}

// NewPi wires the pi subprocess manager; command is the argv to run (nil = the
// built-in fake pi: this binary invoked with the fakepi subcommand).
func NewPi(command []string, notices *Spool) *Pi {
	return &Pi{notices: notices, command: command}
}

// Start launches pi and its stdout/stderr relay pumps. pi_running flips true.
func (p *Pi) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running.Load() {
		return nil
	}
	argv := p.command
	if len(argv) == 0 {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve self for fake pi: %w", err)
		}
		argv = []string{self, "fakepi"}
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
	} else {
		p.trackQueueUpdate(line)
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
	// The pi queue is runtime-only (issue #246 findings): whatever was queued
	// died with the process. Reset the tracker so a later abort does not
	// re-notice already-lost texts.
	p.piQueueMu.Lock()
	p.queuedSteering, p.queuedFollowUp = nil, nil
	p.piQueueMu.Unlock()
}

// queueTexts flattens queue_update elements to human-readable texts: JSON
// strings unwrap to their text, objects stay compact JSON (honest history).
func queueTexts(elems []json.RawMessage) []string {
	texts := make([]string, 0, len(elems))
	for _, e := range elems {
		var s string
		if err := json.Unmarshal(e, &s); err == nil {
			texts = append(texts, s)
			continue
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, e); err == nil {
			texts = append(texts, buf.String())
		} else {
			texts = append(texts, string(e))
		}
	}
	return texts
}

// trackQueueUpdate records queue_update records relayed on stdout into the
// last-known pi queue state (issue #250 / spec #245 E6). queue_update
// carries the queue's full state (issue #246 findings: {steering[],
// followUp[]}); elements are free-form: plain texts become the element
// string, objects keep their compact JSON.
func (p *Pi) trackQueueUpdate(line string) {
	if !strings.Contains(line, `"queue_update"`) {
		return
	}
	var rec struct {
		Type     string            `json:"type"`
		Steering []json.RawMessage `json:"steering"`
		FollowUp []json.RawMessage `json:"followUp"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Type != "queue_update" {
		return
	}
	steer := queueTexts(rec.Steering)
	follow := queueTexts(rec.FollowUp)
	p.piQueueMu.Lock()
	p.queuedSteering, p.queuedFollowUp = steer, follow
	p.piQueueMu.Unlock()
}

// drainNotice spools the queue_drained honest-history notice (spec #245 E6)
// when the tracked queue is non-empty, then clears the tracker. Called before
// clear_queue is sent on the abort path, so the notice lands before the abort
// reaches pi's stdin.
func (p *Pi) drainNotice() {
	p.piQueueMu.Lock()
	steer, follow := p.queuedSteering, p.queuedFollowUp
	p.queuedSteering, p.queuedFollowUp = nil, nil
	p.piQueueMu.Unlock()
	if len(steer) == 0 && len(follow) == 0 {
		return // empty queue: no spurious notice
	}
	n, _ := json.Marshal(map[string]any{
		"type":      "queue_drained",
		"steering":  steer,
		"follow_up": follow,
	})
	if _, err := p.notices.Append(string(n)); err != nil {
		log.Printf("spool append failed: %v", err)
	}
}

// SendCommand writes a command to pi's stdin as one JSON line. A prompt on a
// dead pi respawns it (silent respawn in the stub — the pi_restart notice is
// reserved for Epic 3).
//
// Abort is the Esc mapping (spec #245 E6, issue #250): the drained queue
// texts are spooled as a queue_drained notice, then clear_queue, then abort —
// pi's abort alone continues queued messages, so the order is the semantics.
// A standalone clear_queue command is forwarded as-is (ADR-0083 D4
// break-glass): no notice, no pairing.
func (p *Pi) SendCommand(ctx context.Context, c Command) error {
	if c.Type == CommandAbort {
		if err := p.Start(); err != nil {
			return err
		}
		p.drainNotice()
		clearLine, _ := (json.Marshal)(map[string]any{
			"type":       CommandClearQueue,
			"command_id": c.ID,
		})
		// abort is a bare object with no id field (verified against pi
		// rpc-commands.md; see controller/agent.go piAbortCommand).
		abortLine := []byte(`{"type":"abort"}`)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.stdin == nil {
			return fmt.Errorf("pi stdin unavailable")
		}
		if _, err := p.stdin.Write(append(clearLine, '\n')); err != nil {
			return fmt.Errorf("send clear_queue (abort pair): %w", err)
		}
		_, err := p.stdin.Write(append(abortLine, '\n'))
		return err
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

// Kill terminates the pi process (SIGTERM then SIGKILL); the exit surfaces
// as pi_exit. Not used by the abort front door since Esc-abort (issue #250):
// abort is a pi stdin command pair, not a process kill. Kill stays for
// daemon-side teardown (Epic 3 #248 shutdown/backoff) and tests.
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
// the deterministic four-record turn on stdout. Steer/follow_up enqueue into
// the runtime queue and emit a queue_update carrying the queue's full state;
// abort settles the turn without touching the queue (pi's abort alone
// continues queued messages — the Esc-abort ordering constraint, issue #250);
// clear_queue drains both halves and reports the empty queue. Determinism:
// turn ids are ordinal counters.
func runFakePi(stdin io.Reader, stdout, stderr io.Writer) error {
	out := bufio.NewWriter(stdout)
	defer out.Flush() //nolint:errcheck
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	turn := 0
	var queuedSteering, queuedFollowUp []string
	emitQueue := func() {
		steer := queuedSteering
		if steer == nil {
			steer = []string{}
		}
		follow := queuedFollowUp
		if follow == nil {
			follow = []string{}
		}
		n, _ := json.Marshal(map[string]any{"type": "queue_update", "steering": steer, "followUp": follow})
		out.Write(n)        //nolint:errcheck
		out.WriteByte('\n') //nolint:errcheck
		out.Flush()         //nolint:errcheck
	}
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
		switch c.Type {
		case CommandPrompt:
			// (fall through to the turn emission below)
		case CommandSteer:
			queuedSteering = append(queuedSteering, c.Payload.Message)
			emitQueue()
			continue
		case "follow_up":
			queuedFollowUp = append(queuedFollowUp, c.Payload.Message)
			emitQueue()
			continue
		case CommandAbort:
			// No queue_update: the stub models pi's abort-continues-queue.
			out.Write([]byte(`{"type":"agent_settled","aborted":true}`))
			out.WriteByte('\n') //nolint:errcheck
			out.Flush()         //nolint:errcheck
			continue
		case CommandClearQueue:
			queuedSteering, queuedFollowUp = nil, nil
			emitQueue()
			continue
		default:
			fmt.Fprintf(stderr, "fakepi: unknown command type %q\n", c.Type) //nolint:errcheck
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
