package diag

import (
	"sync"
	"sync/atomic"
	"time"
)

// historyCap bounds the in-memory probe result ring. No disk writes:
// history is a compare-before/after scratchpad, not a log.
const historyCap = 20

// ProbeEntry is one recorded probe result.
type ProbeEntry struct {
	ID      uint64 `json:"id"`
	Kind    string `json:"kind"`
	At      string `json:"at"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
	Payload any    `json:"payload"`
}

// History is a mutex-guarded newest-first ring of probe results.
type History struct {
	mu      sync.Mutex
	entries []ProbeEntry
	nextID  uint64
}

// NewHistory builds an empty history.
func NewHistory() *History { return &History{} }

// Record prepends an entry, evicting the oldest past historyCap.
func (h *History) Record(kind, label, summary string, payload any) ProbeEntry {
	id := atomic.AddUint64(&h.nextID, 1)
	e := ProbeEntry{ID: id, Kind: kind, At: time.Now().UTC().Format(time.RFC3339Nano), Label: label, Summary: summary, Payload: payload}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append([]ProbeEntry{e}, h.entries...)
	if len(h.entries) > historyCap {
		h.entries = h.entries[:historyCap]
	}
	return e
}

// List returns entries newest first.
func (h *History) List() []ProbeEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ProbeEntry, len(h.entries))
	copy(out, h.entries)
	return out
}
