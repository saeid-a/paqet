package forward

import (
	"os"
	"strings"
	"testing"
)

// Return path must mirror udpToStrm success counters, not just IncTarget.
func TestStrmToUDPCountsSuccess(t *testing.T) {
	src, err := os.ReadFile("udp.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	idx := strings.Index(s, "func (f *Forward) strmToUDP")
	if idx < 0 {
		t.Fatal("strmToUDP not found")
	}
	body := s[idx:]
	// cut at next func
	if nxt := strings.Index(body[len("func (f *Forward) strmToUDP"):], "\nfunc "); nxt >= 0 {
		body = body[:len("func (f *Forward) strmToUDP")+nxt]
	}
	for _, want := range []string{
		"IncSuccess()",
		"IncSuccessForwardUDP()",
		"IncSuccessUDP()",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("strmToUDP missing %s; return direction undercounts global success", want)
		}
	}
}

// Drops in serveUDP must be visible as failures.
func TestServeUDPDropsCountFailures(t *testing.T) {
	src, err := os.ReadFile("udp.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	idx := strings.Index(s, "func (f *Forward) serveUDP")
	if idx < 0 {
		t.Fatal("serveUDP not found")
	}
	body := s[idx:]
	if nxt := strings.Index(body[len("func (f *Forward) serveUDP"):], "\nfunc "); nxt >= 0 {
		body = body[:len("func (f *Forward) serveUDP")+nxt]
	}
	if !strings.Contains(body, "IncFailure") {
		t.Fatal("serveUDP drops (oversize/channel-full) skip metrics counting")
	}
}
