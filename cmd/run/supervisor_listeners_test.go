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
	// Real behavior: the TCP sockets actually accept.
	deadline := time.Now().Add(2 * time.Second)
	for _, addr := range []string{"127.0.0.1:18080", "127.0.0.1:18081"} {
		ok := false
		for time.Now().Before(deadline) {
			c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				c.Close()
				ok = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ok {
			t.Errorf("nothing accepting on %s", addr)
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
}
