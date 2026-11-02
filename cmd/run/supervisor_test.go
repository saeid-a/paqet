package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"paqet/internal/conf"
)

// Regression test: stop() during a start retry backoff must cancel the
// in-flight start instead of returning "already stopped" while the core
// still comes up afterwards.
func TestStopCancelsStartBackoff(t *testing.T) {
	iface := "lo0"
	if runtime.GOOS == "linux" {
		iface = "lo"
	}
	cfgYAML := fmt.Sprintf(`role: "client"
log:
  level: "info"
admin:
  enabled: false
socks5:
  - listen: "127.0.0.1:18081"
network:
  interface: "%s"
  ipv4:
    addr: "127.0.0.1:0"
    router_mac: "aa:bb:cc:dd:ee:ff"
server:
  addr: "127.0.0.1:9999"
transport:
  protocol: "kcp"
  conn: 1
  kcp:
    mode: "fast"
    key: "test-secret-key-123"
`, iface)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(cfgYAML), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := conf.LoadFromFile(path); err != nil {
		t.Fatalf("test config invalid: %v", err)
	}

	s := newSupervisor(path, func(_ context.Context, _ *conf.Conf) error {
		return context.DeadlineExceeded // fail fast -> enter retry backoff
	})
	s.SetAutoRestart(true) // 5 attempts, 2s backoff

	startDone := make(chan error, 1)
	go func() { startDone <- s.start() }()

	time.Sleep(500 * time.Millisecond) // let start() enter backoff sleep

	if err := s.stop(); err != nil {
		t.Fatalf("stop during start backoff failed: %v", err)
	}

	select {
	case err := <-startDone:
		if err == nil {
			t.Fatal("start returned nil after stop cancelled it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not abort after stop")
	}
	if s.isRunning() {
		t.Fatal("supervisor reports running after cancelled start")
	}
}
