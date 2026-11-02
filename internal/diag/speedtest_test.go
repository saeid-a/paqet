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
