package diag

import (
	"context"
	"net"
	"testing"
)

// RED: dial probe validates input and clamps timeouts.
func TestDialProbeRejectsBadInput(t *testing.T) {
	if _, err := DialProbe(context.Background(), "", 80, 5); err == nil {
		t.Fatal("empty host must fail")
	}
	if _, err := DialProbe(context.Background(), "example.com", 0, 5); err == nil {
		t.Fatal("port 0 must fail")
	}
	if got := clampProbeTimeout(99, 10); got != 10 {
		t.Fatalf("over-cap must clamp to 10, got %d", got)
	}
	if got := clampProbeTimeout(0, 10); got != 5 {
		t.Fatalf("zero must use default 5, got %d", got)
	}
}

func TestUDPReplyMatchesOnlyPeer(t *testing.T) {
	peer := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 9999}
	same := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 9999}
	other := &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 9999}
	if !matchUDPReply(same, peer) {
		t.Fatal("reply from peer must match")
	}
	if matchUDPReply(other, peer) {
		t.Fatal("reply from wrong source must not match")
	}
	if matchUDPReply(nil, peer) {
		t.Fatal("nil reply must not match")
	}
}
