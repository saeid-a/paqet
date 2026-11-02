package diag

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Speedtest bounds: default 10s, max 10s (handler adds a 2s grace on top
// for the timeout wrapper).
const speedtestDefaultSec = 10
const speedtestMaxSec = 10

// Speedtest modes: which direction counts toward goodput.
const (
	speedtestUp    = "up"
	speedtestDown  = "down"
	speedtestBidir = "bidir"
)

// ValidSpeedtestMode reports whether mode is up|down|bidir.
func ValidSpeedtestMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case speedtestUp, speedtestDown, speedtestBidir:
		return true
	}
	return false
}

// normalizeSpeedtestMode lowercases mode, defaulting empty to up.
func normalizeSpeedtestMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return speedtestUp
	}
	return m
}

// SnapshotCounters samples cumulative tunnel counters: bytes in/out plus
// KCP retx/lost/fec. Wired by the caller (production reads SnapshotNow;
// tests inject a stub).
type SnapshotCounters func() (bytesIn, bytesOut, retx, lost, fec uint64)

// TransferWindow drives the measurement window: it returns when the
// window ends (nil) or aborts early (ctx error). The default
// implementation observes real tunnel traffic for the window; custom
// implementations can generate load. Returned byte counts are advisory —
// Run reports counter deltas.
type TransferWindow func(ctx context.Context, mode string) (up, down uint64, err error)

// SpeedtestResult reports observed tunnel goodput over the window plus
// the KCP counter delta sampled across it.
type SpeedtestResult struct {
	DurationSec int               `json:"duration_sec"`
	Mode        string            `json:"mode"`
	BytesUp     uint64            `json:"bytes_up"`
	BytesDown   uint64            `json:"bytes_down"`
	GoodputMbps float64           `json:"goodput_mbps"`
	KCPDelta    map[string]uint64 `json:"kcp_delta"`
}

// Sentinel speedtest failures.
var (
	ErrBusy     = fmt.Errorf("speedtest already running")
	ErrNoTunnel = fmt.Errorf("no active tunnel")
)

// SpeedtestManager runs one speedtest at a time.
type SpeedtestManager struct {
	mu       sync.Mutex
	snapshot SnapshotCounters
	transfer TransferWindow
	active   atomic.Bool
}

// NewSpeedtestManager builds a manager around snapshot.
func NewSpeedtestManager(snapshot SnapshotCounters) *SpeedtestManager {
	m := &SpeedtestManager{snapshot: snapshot}
	m.transfer = func(ctx context.Context, mode string) (uint64, uint64, error) {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			return 0, 0, nil
		}
		return 0, 0, ctx.Err()
	}
	return m
}

// SetActive marks whether a live tunnel exists. The handler refreshes it
// from SnapshotNow before each run; without a tunnel Run refuses with
// ErrNoTunnel instead of reporting fake numbers.
func (m *SpeedtestManager) SetActive(active bool) { m.active.Store(active) }

// SetTransfer overrides the window driver (tests, future load gen).
func (m *SpeedtestManager) SetTransfer(t TransferWindow) { m.transfer = t }

// Run measures tunnel goodput for durationSec in mode.
func (m *SpeedtestManager) Run(ctx context.Context, durationSec int, mode string) (SpeedtestResult, error) {
	mode = normalizeSpeedtestMode(mode)
	if !ValidSpeedtestMode(mode) {
		return SpeedtestResult{}, fmt.Errorf("unknown mode (want up|down|bidir)")
	}
	if durationSec <= 0 {
		durationSec = speedtestDefaultSec
	}
	if durationSec > speedtestMaxSec {
		durationSec = speedtestMaxSec
	}
	if !m.mu.TryLock() {
		return SpeedtestResult{}, ErrBusy
	}
	defer m.mu.Unlock()
	if !m.active.Load() {
		return SpeedtestResult{}, ErrNoTunnel
	}
	in0, out0, retx0, lost0, fec0 := m.snapshot()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(durationSec)*time.Second)
	defer cancel()
	if _, _, err := m.transfer(runCtx, mode); err != nil {
		return SpeedtestResult{}, err
	}
	in1, out1, retx1, lost1, fec1 := m.snapshot()
	up, down := out1-out0, in1-in0
	counted := up + down
	switch mode {
	case speedtestUp:
		counted = up
	case speedtestDown:
		counted = down
	}
	return SpeedtestResult{
		DurationSec: durationSec,
		Mode:        mode,
		BytesUp:     up,
		BytesDown:   down,
		GoodputMbps: float64(counted*8) / 1e6 / float64(durationSec),
		KCPDelta: map[string]uint64{
			"retransmits":   retx1 - retx0,
			"lost_segs":     lost1 - lost0,
			"fec_recovered": fec1 - fec0,
		},
	}, nil
}
