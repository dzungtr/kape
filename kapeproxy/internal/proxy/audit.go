package proxy

import "github.com/rs/zerolog"

// AuditEntry is one tool-call audit record.
type AuditEntry struct {
	NamespacedName string
	Upstream       string
	OriginalName   string
	Allowed        bool
	LatencyMS      int64
	Error          string // empty when no error
	TaskID         string // empty when no kape task context
}

// AuditLogger writes one structured log line per tool call.
type AuditLogger struct {
	logger zerolog.Logger
}

// NewAuditLogger constructs an AuditLogger writing to the given zerolog logger.
func NewAuditLogger(logger zerolog.Logger) *AuditLogger {
	return &AuditLogger{logger: logger}
}

// Log writes the entry unconditionally.
func (a *AuditLogger) Log(e AuditEntry) {
	ev := a.logger.Info().
		Str("tool.namespaced_name", e.NamespacedName).
		Str("tool.upstream", e.Upstream).
		Str("tool.original_name", e.OriginalName).
		Bool("tool.allowed", e.Allowed).
		Int64("tool.latency_ms", e.LatencyMS)
	if e.Error != "" {
		ev = ev.Str("error", e.Error)
	}
	if e.TaskID != "" {
		ev = ev.Str("kape.task_id", e.TaskID)
	}
	ev.Msg("kapeproxy.tool_call")
}

// LogIfEnabled writes the entry only when enabled is true.
func (a *AuditLogger) LogIfEnabled(enabled bool, e AuditEntry) {
	if enabled {
		a.Log(e)
	}
}
