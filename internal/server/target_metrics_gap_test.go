package server

import (
	"os"
	"strings"
	"testing"
)

func TestTCPFeedsTargets(t *testing.T) {
	src, _ := os.ReadFile("tcp.go")
	if !strings.Contains(string(src), "IncTarget") {
		t.Fatal("handleTCP missing IncTarget; server target bytes stay 0")
	}
}

func TestUDPFeedsTargets(t *testing.T) {
	src, _ := os.ReadFile("udp.go")
	if !strings.Contains(string(src), "IncTarget") {
		t.Fatal("handleUDP missing IncTarget; server target bytes stay 0")
	}
}
