package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Spool is the durable unacked-record log on the sandbox PVC (ADR-0083 D1/D3):
// an append-only JSONL file of `{spool_id, data}` envelopes. `data` is the
// verbatim pi JSONL record (never re-wrapped — filtering stays serving-time)
// or a daemon notice. The spool is the id authority while records are unacked;
// the controller stays the event-store seq authority.
//
// seq semantics mirror the controller EventStore's recover-from-file pattern
// (controller/eventstore.go NextSeq): the highest stored spool_id is the tail,
// so ids continue across daemon restarts with no reset and no reuse.
type Spool struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	next    uint64 // next spool_id to assign
	maxRecs int    // cap; oldest records dropped (with a notice) beyond it
	recs    []spoolEntry
	waiters map[chan struct{}]struct{}
}

// spoolEntry is the on-disk envelope: data is raw JSON text, stored as-is.
type spoolEntry struct {
	SpoolID uint64 `json:"spool_id"`
	Data    string `json:"data"`
}

// Record is what GET /records serves: the envelope with data as
// json.RawMessage so the controller receives the verbatim pi record.
type Record struct {
	SpoolID uint64          `json:"spool_id"`
	Data    json.RawMessage `json:"data"`
}

// NewSpool opens (creating if needed) the spool file at path and recovers the
// next spool_id from the stored tail.
func NewSpool(path string, maxRecords int) (*Spool, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("spool dir %s: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open spool %s: %w", path, err)
	}
	s := &Spool{path: path, f: f, maxRecs: maxRecords, next: 1, waiters: map[chan struct{}]struct{}{}} // ids start at 1
	// Recover-from-file: scan the whole log; the highest spool_id is the tail.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e spoolEntry
		if err := json.Unmarshal(line, &e); err != nil {
			f.Close()
			return nil, fmt.Errorf("spool %s: corrupt record at recovery: %w", path, err)
		}
		s.recs = append(s.recs, e)
		if e.SpoolID >= s.next {
			s.next = e.SpoolID + 1
		}
	}
	if err := sc.Err(); err != nil {
		f.Close()
		return nil, fmt.Errorf("scan spool %s: %w", path, err)
	}
	return s, nil
}

// appendLocked durably stores the envelope under a held lock.
func (s *Spool) appendLocked(e spoolEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := s.f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("spool append: %w", err)
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("spool sync: %w", err)
	}
	s.recs = append(s.recs, e)
	s.next = e.SpoolID + 1
	return nil
}

// Append durably stores data (a raw JSON record: pi output or a daemon
// notice), assigning the next monotonic spool_id. When the cap is exceeded the
// oldest records are dropped and a spool_truncated notice is appended so the
// controller learns of the loss.
func (s *Spool) Append(data string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next // spool ids start at 1, like the controller's seq
	if err := s.appendLocked(spoolEntry{SpoolID: id, Data: data}); err != nil {
		return Record{}, err
	}
	truncated := false
	for len(s.recs) > s.maxRecs {
		s.recs = s.recs[len(s.recs)-s.maxRecs:]
		truncated = true
	}
	if truncated {
		notice, _ := json.Marshal(map[string]string{"type": "spool_truncated"})
		// The notice itself is appended and always retained (one per
		// truncation event; a fresh cap of >=1 keeps it in the window).
		if err := s.appendLocked(spoolEntry{SpoolID: s.next, Data: string(notice)}); err != nil {
			return Record{}, err
		}
	}
	// Wake long-pollers: arrival means any /records call returns immediately.
	for ch := range s.waiters {
		close(ch)
	}
	s.waiters = map[chan struct{}]struct{}{}
	return Record{SpoolID: id, Data: json.RawMessage(data)}, nil
}

// Read returns every record with spool_id > after, ascending. With after at
// the tail it blocks until a new record arrives or the wait expires, then
// returns an empty list (the empty poll is the heartbeat, ADR-0083 D2/D6).
func (s *Spool) Read(after uint64, wait time.Duration) []Record {
	s.mu.Lock()
	if idx := findAfter(s.recs, after); idx < len(s.recs) {
		out := toRecords(s.recs[idx:])
		s.mu.Unlock()
		return out
	}
	if wait <= 0 {
		s.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	s.waiters[ch] = struct{}{}
	s.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(wait):
	}
	s.mu.Lock()
	delete(s.waiters, ch)
	out := toRecords(s.recs[findAfter(s.recs, after):])
	s.mu.Unlock()
	return out
}

func findAfter(recs []spoolEntry, after uint64) int {
	for i, e := range recs {
		if e.SpoolID > after {
			return i
		}
	}
	return len(recs)
}

func toRecords(recs []spoolEntry) []Record {
	out := make([]Record, len(recs))
	for i, e := range recs {
		out[i] = Record{SpoolID: e.SpoolID, Data: json.RawMessage(e.Data)}
	}
	return out
}

// Close releases the file.
func (s *Spool) Close() error { return s.f.Close() }
