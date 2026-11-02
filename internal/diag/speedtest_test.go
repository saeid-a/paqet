package diag

import (
	"context"
	"testing"
	"time"
)

// RED: idle tunnel refuses with ErrNoTunnel; a second concurrent run gets
// ErrBusy while the first holds the lock.
func TestSpeedtestSingleFlight(t *testing.T) {
	m := NewSpeedtestManager(func() (uint64, uint64, uint64, uint64, uint64) {
		return 0, 0, 0, 0, 0
	})
	m.SetActive(false)
	if _, err := m.Run(context.Background(), 1, "up"); err != ErrNoTunnel {
		t.Fatalf("idle must return ErrNoTunnel, got %v", err)
	}
	m.SetActive(true)
	m.SetTransfer(func(ctx context.Context, mode string) (up, down uint64, err error) {
		<-ctx.Done()
		return 0, 0, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.Run(ctx, 10, "up")
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := m.Run(context.Background(), 1, "up"); err != ErrBusy {
		t.Fatalf("concurrent must return ErrBusy, got %v", err)
	}
	cancel()
	<-done
}

func TestSpeedtestClampsCounterReset(t *testing.T) {
	calls := 0
	m := NewSpeedtestManager(func() (uint64, uint64, uint64, uint64, uint64) {
		calls++
		if calls == 1 {
			return 1000, 2000, 7, 3, 1
		}
		return 10, 20, 0, 0, 0
	})
	m.SetActive(true)
	m.SetTransfer(func(ctx context.Context, mode string) (uint64, uint64, error) { return 0, 0, nil })
	res, err := m.Run(context.Background(), 1, "up")
	if err != nil {
		t.Fatalf("reset must not error, got %v", err)
	}
	if res.BytesUp != 0 || res.BytesDown != 0 {
		t.Fatalf("reset counters must clamp to 0, got up=%d down=%d", res.BytesUp, res.BytesDown)
	}
	if res.GoodputMbps != 0 {
		t.Fatalf("reset counters must report 0 goodput, got %f", res.GoodputMbps)
	}
	if res.KCPDelta["retransmits"] != 0 || res.KCPDelta["lost_segs"] != 0 || res.KCPDelta["fec_recovered"] != 0 {
		t.Fatalf("reset KCP counters must clamp to 0, got %v", res.KCPDelta)
	}
}
