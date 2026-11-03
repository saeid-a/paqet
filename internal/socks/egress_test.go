package socks

import (
	"testing"
)

func TestEffectiveTargetDirect(t *testing.T) {
	s := &Server{}
	if got := s.effectiveTarget("example.com:80"); got != "example.com:80" {
		t.Fatalf("got %q", got)
	}
}

func TestEffectiveTargetForced(t *testing.T) {
	s := &Server{}
	s.SetEgress("127.0.0.1:5005")
	if got := s.Egress(); got != "127.0.0.1:5005" {
		t.Fatalf("egress = %q", got)
	}
	if got := s.effectiveTarget("example.com:80"); got != "127.0.0.1:5005" {
		t.Fatalf("forced target = %q", got)
	}
	s.SetEgress("")
	if got := s.effectiveTarget("example.com:80"); got != "example.com:80" {
		t.Fatalf("cleared target = %q", got)
	}
}

func TestEffectiveTargetDynamicResolver(t *testing.T) {
	s := &Server{}
	s.SetEgressSpec("@active", func() string { return "127.0.0.1:5005" })
	if got := s.Egress(); got != "@active" {
		t.Fatalf("egress spec = %q", got)
	}
	if got := s.effectiveTarget("example.com:80"); got != "127.0.0.1:5005" {
		t.Fatalf("resolved target = %q", got)
	}
	s.SetEgressSpec("@active", func() string { return "" })
	if got := s.effectiveTarget("example.com:80"); got != "example.com:80" {
		t.Fatalf("unresolved falls back to direct, got %q", got)
	}
}
