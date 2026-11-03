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

func TestTunnelProbeUnwired(t *testing.T) {
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	_, _, err := sup.probeViaTunnel(context.Background(), "127.0.0.1:9", 10)
	if err == nil {
		t.Fatal("want error when no probe fn set, got nil")
	}
}

func TestTunnelProbeDelegates(t *testing.T) {
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	sup.setTunnelProbe(func(ctx context.Context, target string, d time.Duration) (int64, bool, error) {
		if target != "127.0.0.1:9" {
			t.Errorf("target = %q", target)
		}
		if d != 7*time.Second {
			t.Errorf("timeout = %v", d)
		}
		return 42, true, nil
	})
	ms, spoke, err := sup.probeViaTunnel(context.Background(), "127.0.0.1:9", 7)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if ms != 42 || !spoke {
		t.Fatalf("got (%d, %v)", ms, spoke)
	}
}

func TestSocksEgressUnwired(t *testing.T) {
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	if err := sup.SetSocksEgress("socks5:127.0.0.1:1080", "@active"); err == nil {
		t.Fatal("want error for unknown listener, got nil")
	}
}

func TestSocksEgressRejectsBadSpec(t *testing.T) {
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	sup.trackSocksServer("socks5:127.0.0.1:1080", nil, "")
	if err := sup.SetSocksEgress("socks5:127.0.0.1:1080", "@bogus"); err == nil {
		t.Fatal("want error for bad spec, got nil")
	}
	if err := sup.SetSocksEgress("forward:127.0.0.1:18081", "@active"); err == nil {
		t.Fatal("want error for non-socks listener, got nil")
	}
}

func TestSocksEgressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sup := newSupervisor(dir+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	sup.trackSocksServer("socks5:127.0.0.1:1080", nil, "")
	if err := sup.SetSocksEgress("socks5:127.0.0.1:1080", "127.0.0.1:5005"); err != nil {
		t.Fatalf("set: %v", err)
	}
	spec, resolved := sup.SocksEgress("socks5:127.0.0.1:1080")
	if spec != "127.0.0.1:5005" || resolved != "127.0.0.1:5005" {
		t.Fatalf("got (%q, %q)", spec, resolved)
	}
}

func TestResolveSocksEgressPinned(t *testing.T) {
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	sup.setForwardTargets(map[string]string{"127.0.0.1:18081": "127.0.0.1:5005"})
	if got := sup.resolveSocksEgress("forward:127.0.0.1:18081"); got != "127.0.0.1:5005" {
		t.Fatalf("pinned = %q", got)
	}
	if got := sup.resolveSocksEgress("forward:127.0.0.1:9"); got != "" {
		t.Fatalf("unknown forward = %q, want empty", got)
	}
	if got := sup.resolveSocksEgress(""); got != "" {
		t.Fatalf("direct = %q, want empty", got)
	}
	if got := sup.resolveSocksEgress("127.0.0.1:5005"); got != "127.0.0.1:5005" {
		t.Fatalf("custom = %q", got)
	}
}

func TestResolveSocksEgressFollowsActive(t *testing.T) {
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	sup.setForwardTargets(map[string]string{
		"127.0.0.1:18081": "127.0.0.1:5005",
		"127.0.0.1:18082": "127.0.0.1:6006",
	})
	noop := func(ctx context.Context) error { <-ctx.Done(); return nil }
	// No forwards running: unresolvable, direct fallback.
	if got := sup.resolveSocksEgress("@active"); got != "" {
		t.Fatalf("no forwards = %q, want empty", got)
	}
	sup.RegisterListener("forward:127.0.0.1:18081", noop)
	sup.RegisterListener("forward:127.0.0.1:18082", noop)
	if err := sup.StartListener("forward:127.0.0.1:18081"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := sup.resolveSocksEgress("@active"); got != "127.0.0.1:5005" {
		t.Fatalf("sole forward = %q", got)
	}
	if err := sup.StartListener("forward:127.0.0.1:18082"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := sup.resolveSocksEgress("@active"); got != "" {
		t.Fatalf("two forwards = %q, want empty (ambiguous)", got)
	}
	if err := sup.StopListener("forward:127.0.0.1:18082"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := sup.resolveSocksEgress("@active"); got != "127.0.0.1:5005" {
		t.Fatalf("after stop = %q", got)
	}
}
