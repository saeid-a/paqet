package kcp

import (
	"os"
	"strings"
	"testing"
)

func TestDialFailuresCounted(t *testing.T) {
	src, _ := os.ReadFile("dial.go")
	if !strings.Contains(string(src), "IncFailure") {
		t.Fatal("Dial failures (packetconn/handshake/NewConn/smux) skip metrics counting")
	}
}

func TestListenAcceptFailuresCounted(t *testing.T) {
	src, _ := os.ReadFile("listen.go")
	// Accept() has two failure returns; must count, not just success IncConnections
	idx := strings.Index(string(src), "func (l *Listener) Accept()")
	if idx < 0 {
		t.Fatal("Accept not found")
	}
	body := string(src)[idx:]
	if !strings.Contains(body, "IncFailure") {
		t.Fatal("Accept failures skip metrics counting")
	}
}

func TestOpenAcceptStrmFailuresCounted(t *testing.T) {
	src, _ := os.ReadFile("conn.go")
	for _, name := range []string{"func (c *Conn) OpenStrm()", "func (c *Conn) AcceptStrm()"} {
		idx := strings.Index(string(src), name)
		if idx < 0 {
			t.Fatalf("%s not found", name)
		}
		rest := string(src)[idx+len(name):]
		if nxt := strings.Index(rest, "\nfunc "); nxt >= 0 {
			rest = rest[:nxt]
		}
		if !strings.Contains(rest, "IncFailure") {
			t.Fatalf("%s failure skips metrics counting", name)
		}
	}
}
