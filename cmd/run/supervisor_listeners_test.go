package run

import (
	"context"
	"net"
	"testing"
	"time"

	"paqet/internal/client"
	"paqet/internal/conf"
)

func mustUDPAddr(t *testing.T, s string) *net.UDPAddr {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		t.Fatalf("resolve %s: %v", s, err)
	}
	return a
}

func TestDataPlaneListenersRegistered(t *testing.T) {
	cfg := &conf.Conf{
		SOCKS5: []conf.SOCKS5{
			{Listen_: "127.0.0.1:18080"},
		},
		Forward: []conf.Forward{
			{Listen_: "127.0.0.1:18081", Target: "127.0.0.1:9", Protocol: "tcp"},
		},
	}
	for i := range cfg.SOCKS5 {
		a, err := net.ResolveUDPAddr("udp", cfg.SOCKS5[i].Listen_)
		if err != nil {
			t.Fatalf("resolve socks listen: %v", err)
		}
		cfg.SOCKS5[i].Listen = a
	}
	for i := range cfg.Forward {
		a, err := net.ResolveUDPAddr("udp", cfg.Forward[i].Listen_)
		if err != nil {
			t.Fatalf("resolve forward listen: %v", err)
		}
		cfg.Forward[i].Listen = a
	}

	coreCtx, coreCancel := context.WithCancel(context.Background())
	defer coreCancel()
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	cl, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if err := registerDataPlaneListeners(coreCtx, sup, cl, cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	want := map[string]bool{"socks5:127.0.0.1:18080": false, "forward:127.0.0.1:18081": false}
	for _, n := range sup.ListListeners() {
		if _, ok := want[n]; ok {
			want[n] = true
		} else {
			t.Errorf("unexpected listener %q", n)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing listener %q", id)
		}
	}

	for id := range want {
		if err := sup.StartListener(id); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
	}
	// Both wrappers block while serving, so running state is deterministic.
	for id := range want {
		if !sup.ListenerRunning(id) {
			t.Errorf("listener %s not running after start", id)
		}
	}
	// Bound sockets reject a second bind; no data flows (the upstream
	// client is unstarted by design, so dialing the data path is out of
	// scope for this lifecycle test). Binding is async, so poll.
	deadline := time.Now().Add(10 * time.Second)
	for _, addr := range []string{"127.0.0.1:18080", "127.0.0.1:18081"} {
		held := false
		for time.Now().Before(deadline) {
			l, err := net.Listen("tcp", addr)
			if err == nil {
				l.Close()
				time.Sleep(50 * time.Millisecond)
				continue
			}
			held = true
			break
		}
		if !held {
			t.Errorf("second bind on %s kept succeeding, listener not holding the port", addr)
		}
	}
	for id := range want {
		if err := sup.StopListener(id); err != nil {
			t.Fatalf("stop %s: %v", id, err)
		}
		if sup.ListenerRunning(id) {
			t.Errorf("listener %s still running after stop", id)
		}
	}
	// After stop the ports must be released (close propagates async).
	deadline = time.Now().Add(5 * time.Second)
	for _, addr := range []string{"127.0.0.1:18080", "127.0.0.1:18081"} {
		for {
			l, err := net.Listen("tcp", addr)
			if err == nil {
				l.Close()
				break
			}
			if !time.Now().Before(deadline) {
				t.Errorf("port %s still bound after stop", addr)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func testRestartCfg(t *testing.T) *conf.Conf {
	t.Helper()
	cfg := &conf.Conf{
		SOCKS5: []conf.SOCKS5{
			{Listen_: "127.0.0.1:18083"},
		},
		Forward: []conf.Forward{
			{Listen_: "127.0.0.1:18084", Target: "127.0.0.1:9", Protocol: "tcp"},
		},
	}
	for i := range cfg.SOCKS5 {
		a, err := net.ResolveUDPAddr("udp", cfg.SOCKS5[i].Listen_)
		if err != nil {
			t.Fatalf("resolve socks listen: %v", err)
		}
		cfg.SOCKS5[i].Listen = a
	}
	for i := range cfg.Forward {
		a, err := net.ResolveUDPAddr("udp", cfg.Forward[i].Listen_)
		if err != nil {
			t.Fatalf("resolve forward listen: %v", err)
		}
		cfg.Forward[i].Listen = a
	}
	return cfg
}

func waitListenerRunning(t *testing.T, sup *supervisor, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if sup.ListenerRunning(id) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("listener %s never reported running", id)
}

// TestRestartReregistersListeners simulates a core restart: the start
// sequence runs, the core context is cancelled (stop), and the sequence
// runs again immediately — while the old generation may still be marked
// running. The second generation must succeed, not fail with
// "already running".
func TestRestartReregistersListeners(t *testing.T) {
	cfg := testRestartCfg(t)
	coreCtx, coreCancel := context.WithCancel(context.Background())
	defer coreCancel()
	sup := newSupervisor(t.TempDir()+"/client.yaml", func(ctx context.Context, c *conf.Conf) error { return nil })
	cl, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if err := startDataPlaneListeners(coreCtx, sup, cl, cfg); err != nil {
		t.Fatalf("first generation: %v", err)
	}
	waitListenerRunning(t, sup, "socks5:127.0.0.1:18083")
	waitListenerRunning(t, sup, "forward:127.0.0.1:18084")

	coreCancel() // restart stops the core, then immediately restarts it
	if err := startDataPlaneListeners(context.Background(), sup, cl, cfg); err != nil {
		t.Fatalf("second generation: %v", err)
	}
	waitListenerRunning(t, sup, "socks5:127.0.0.1:18083")
	waitListenerRunning(t, sup, "forward:127.0.0.1:18084")

	// The new generation owns the ports (second bind must fail once the
	// async bind lands). No data flows: the upstream client is unstarted
	// by design.
	deadline := time.Now().Add(15 * time.Second)
	for _, addr := range []string{"127.0.0.1:18083", "127.0.0.1:18084"} {
		held := false
		for time.Now().Before(deadline) {
			l, err := net.Listen("tcp", addr)
			if err == nil {
				l.Close()
				time.Sleep(100 * time.Millisecond)
				continue
			}
			held = true
			break
		}
		if !held {
			t.Errorf("second bind on %s kept succeeding after restart, new generation not holding the port", addr)
		}
	}
}
