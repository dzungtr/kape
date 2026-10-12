package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

// EventStore is the durable append-only event log behind the EventHub
// (issue #195). The interface hides the storage choice; the hub is the
// single writer per agent, and the controller is the single writer of the
// file — no other process touches the store.
//
// Semantics (recorded in spec #194 Handoffs):
//   - seq: per-agent monotonic counter starting at 1, assigned at append
//     time, no gaps and no reuse across restart (recovered from the stored
//     log's highest key).
//   - cursor: the last seq a consumer has seen; replay returns every event
//     with seq > cursor in ascending order, so cursors survive restarts
//     exactly as long as the log does.
type EventStore interface {
	// NextSeq returns the highest seq assigned to the agent (0 if none).
	NextSeq(agentID string) (uint64, error)
	// Append durably stores ev before any fan-out. The caller (the hub) has
	// already assigned ev.Seq from NextSeq; Append rejects a seq that does
	// not advance the log so a miscounting writer can never create a gap.
	Append(agentID string, ev Event) error
	// AppendWithOrigin is Append plus the origin watermark sidecar (issue
	// #212): the event and the source daemon spool_id commit in one
	// transaction, so a controller crash can never leave an event appended
	// without its watermark (or vice versa) — the restart re-pull starts
	// from the watermark and cannot duplicate or skip a record.
	AppendWithOrigin(agentID string, ev Event, originSpoolID uint64) error
	// OriginWatermark returns the agent's highest appended origin spool_id
	// (0 if none) — the ack cursor the puller resumes from after restart.
	OriginWatermark(agentID string) (uint64, error)
	// Replay returns all events with seq > after, ascending.
	Replay(agentID string, after uint64) ([]Event, error)
	// Agents lists the agent ids that have a log (the replay enumeration
	// for registry startup, issue #197).
	Agents() ([]string, error)
	// Close releases the underlying file.
	Close() error
}

// BoltEventStore is the EventStore implementation: an embedded single-file
// bbolt database on a controller-local volume (PVC in-cluster, KAPE_EVENT_STORE).
// One bucket per agent, keyed by agent id; the key is the 8-byte big-endian
// seq, so per-agent seq order is the key order and NextSeq is a max-key read.
// Each Append is one bbolt Update transaction — committed or not, which makes
// replay after an unclean shutdown safe (the last committed seq is the tail).
type BoltEventStore struct {
	db *bolt.DB
}

var agentBucketSeqWidth = 8

// originBucket stores the per-agent origin watermark sidecar (issue #212):
// key agentID, value 8-byte big-endian spool_id of the highest durably
// appended pulled record.
var originBucket = []byte("origin-wm")

func seqKey(seq uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, seq)
}

// NewBoltEventStore opens (creating if needed) the single-file store at path.
func NewBoltEventStore(path string) (*BoltEventStore, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("event store dir %s: %w", dir, err)
		}
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open event store %s: %w", path, err)
	}
	return &BoltEventStore{db: db}, nil
}

// NextSeq reads the agent's highest stored seq.
func (s *BoltEventStore) NextSeq(agentID string) (uint64, error) {
	var last uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(agentID))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		if k, _ := c.Last(); k != nil {
			if len(k) != agentBucketSeqWidth {
				return fmt.Errorf("agent %s: malformed seq key", agentID)
			}
			last = binary.BigEndian.Uint64(k)
		}
		return nil
	})
	return last, err
}

// Append writes ev into the agent's bucket, creating the bucket on first
// append. Transactional: seq ordering and durability commit together.
func (s *BoltEventStore) Append(agentID string, ev Event) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return appendInTx(tx, agentID, ev)
	})
}

// AppendWithOrigin appends the event and advances the origin watermark in
// the same Update transaction (issue #212) — atomic with the append.
func (s *BoltEventStore) AppendWithOrigin(agentID string, ev Event, originSpoolID uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := appendInTx(tx, agentID, ev); err != nil {
			return err
		}
		wm, err := tx.CreateBucketIfNotExists(originBucket)
		if err != nil {
			return err
		}
		// Watermarks are monotonic per agent; never let a stale pull lower it.
		if cur := wm.Get([]byte(agentID)); len(cur) == 8 && binary.BigEndian.Uint64(cur) > originSpoolID {
			return nil
		}
		return wm.Put([]byte(agentID), seqKey(originSpoolID))
	})
}

// OriginWatermark reads the agent's highest appended origin spool_id.
func (s *BoltEventStore) OriginWatermark(agentID string) (uint64, error) {
	var out uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		wm := tx.Bucket(originBucket)
		if wm == nil {
			return nil
		}
		if v := wm.Get([]byte(agentID)); len(v) == 8 {
			out = binary.BigEndian.Uint64(v)
		}
		return nil
	})
	return out, err
}

// appendInTx is the shared seq-advance-guarded put. Caller holds an Update tx.
func appendInTx(tx *bolt.Tx, agentID string, ev Event) error {
	b, err := tx.CreateBucketIfNotExists([]byte(agentID))
	if err != nil {
		return err
	}
	c := b.Cursor()
	var last uint64
	if k, _ := c.Last(); k != nil {
		last = binary.BigEndian.Uint64(k)
	}
	if ev.Seq != last+1 {
		return fmt.Errorf("agent %s: append seq %d does not advance log (last %d)", agentID, ev.Seq, last)
	}
	v, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return b.Put(seqKey(ev.Seq), v)
}

// Replay returns every stored event with seq > after, ascending by key.
func (s *BoltEventStore) Replay(agentID string, after uint64) ([]Event, error) {
	var out []Event
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(agentID))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			if len(k) != agentBucketSeqWidth {
				return fmt.Errorf("agent %s: malformed seq key", agentID)
			}
			if seq := binary.BigEndian.Uint64(k); seq > after {
				var ev Event
				if err := json.Unmarshal(v, &ev); err != nil {
					return fmt.Errorf("agent %s seq %d: %w", agentID, seq, err)
				}
				out = append(out, ev)
			}
			return nil
		})
	})
	return out, err
}

// Close closes the database file.
func (s *BoltEventStore) Close() error { return s.db.Close() }

// Agents lists every agent bucket name — one bucket per agent id.
func (s *BoltEventStore) Agents() ([]string, error) {
	var out []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			if string(name) == string(originBucket) {
				return nil // the watermark sidecar, not an agent log (issue #212)
			}
			out = append(out, string(name))
			return nil
		})
	})
	return out, err
}
