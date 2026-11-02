package socks

import (
	"os"
	"strings"
	"testing"
)

func funcBody(src, name string) string {
	idx := strings.Index(src, name)
	if idx < 0 {
		return ""
	}
	body := src[idx:]
	rest := body[len(name):]
	if nxt := strings.Index(rest, "\nfunc "); nxt >= 0 {
		body = body[:len(name)+nxt]
	}
	return body
}

func TestStrmToUDPCountsSuccess(t *testing.T) {
	src, err := os.ReadFile("associate.go")
	if err != nil {
		t.Fatal(err)
	}
	body := funcBody(string(src), "func (s *Server) strmToUDP")
	if body == "" {
		t.Fatal("strmToUDP not found")
	}
	for _, want := range []string{
		"IncSuccess()",
		"IncSuccessUDP()",
		"IncSuccessSOCKS()",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("strmToUDP missing %s; return direction undercounts global success", want)
		}
	}
}

func TestServeUDPAndPayloadTooLargeCountFailures(t *testing.T) {
	src, err := os.ReadFile("associate.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	serve := funcBody(s, "func (s *Server) serveUDP")
	if serve == "" {
		t.Fatal("serveUDP not found")
	}
	if !strings.Contains(serve, "IncFailure") {
		t.Fatal("serveUDP drops (oversize/wrong-source/malformed) skip metrics counting")
	}
	udpTo := funcBody(s, "func (s *Server) udpToStrm")
	if udpTo == "" {
		t.Fatal("udpToStrm not found")
	}
	// payload-too-large early return must count
	idx := strings.Index(udpTo, "payload too large")
	if idx < 0 {
		t.Fatal("payload-too-large branch not found")
	}
	// slice from branch to next metrics/return: require IncFailure nearby
	branch := udpTo[idx:]
	if end := strings.Index(branch, "return"); end >= 0 {
		branch = branch[:end]
	}
	if !strings.Contains(branch, "IncFailure") && !strings.Contains(udpTo, "IncFailure") {
		t.Fatal("payload-too-large skips metrics counting")
	}
	// More precise: the early return block itself must contain metrics.
	// udpToStrm has metrics later, so check the specific block.
	if !strings.Contains(branch, "metrics.") {
		t.Fatalf("payload-too-large early return has no metrics call")
	}
}

func TestHandshakeFailuresCount(t *testing.T) {
	src, err := os.ReadFile("socks.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "metrics.") {
		t.Fatal("socks.go handshake failures (negotiate/read/unsupported-cmd) skip metrics counting")
	}
}
