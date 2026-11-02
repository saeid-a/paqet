// Package diag probe funcs power the Phase 2 probe lab. All probes are
// unprivileged, timeout-bounded, and abort on ctx cancellation. No raw
// sockets, no disk writes.
package diag

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Timeout bounds for the probe lab. Over-cap values clamp to the max;
// zero/negative values use the default. Matches the ping handler pattern
// (default 10, max 10) with shorter probe defaults.
const (
	probeDefaultTimeout = 5
	probeMaxTimeout     = 10
)

// clampProbeTimeout bounds v to [1, max]; zero/negative uses def.
func clampProbeTimeout(v, max int) int {
	if max <= 0 {
		max = probeMaxTimeout
	}
	if v <= 0 {
		if max >= probeDefaultTimeout {
			return probeDefaultTimeout
		}
		return max
	}
	if v > max {
		return max
	}
	return v
}

// validateProbeTarget rejects empty/overlong hosts and out-of-range ports.
func validateProbeTarget(host string, port int) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("missing host")
	}
	if len(host) > 253 {
		return fmt.Errorf("host too long")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port (want 1-65535)")
	}
	return nil
}

// DialResult is the outcome of one TCP dial probe.
type DialResult struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	OK        bool   `json:"ok"`
	ConnectMs int64  `json:"connect_ms"`
	Error     string `json:"error,omitempty"`
}

// DialProbe dials TCP to host:port and reports connect timing.
func DialProbe(ctx context.Context, host string, port, timeoutSec int) (DialResult, error) {
	host = strings.TrimSpace(host)
	if err := validateProbeTarget(host, port); err != nil {
		return DialResult{}, err
	}
	timeoutSec = clampProbeTimeout(timeoutSec, probeMaxTimeout)
	d := net.Dialer{Timeout: time.Duration(timeoutSec) * time.Second}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return DialResult{Host: host, Port: port, OK: false, ConnectMs: ms, Error: err.Error()}, nil
	}
	_ = conn.Close()
	return DialResult{Host: host, Port: port, OK: true, ConnectMs: ms}, nil
}

// UDP echo bounds: nonce size default and cap.
const (
	udpDefaultBytes = 32
	udpMaxBytes     = 1200
)

// UDPResult is the outcome of one UDP echo probe.
type UDPResult struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	OK    bool   `json:"ok"`
	RttMs int64  `json:"rtt_ms"`
	Note  string `json:"note,omitempty"`
	Error string `json:"error,omitempty"`
}

// matchUDPReply reports whether a reply came from the probed peer.
// Connected sockets only deliver peer traffic, but the check stays
// explicit so stray-packet handling never silently widens.
func matchUDPReply(from, peer *net.UDPAddr) bool {
	if from == nil || peer == nil {
		return false
	}
	if from.Port != peer.Port {
		return false
	}
	if len(from.IP) == 0 || len(peer.IP) == 0 {
		return false
	}
	return from.IP.Equal(peer.IP)
}

// UDPProbe sends a nonce datagram to host:port and waits for a reply.
// A timeout reports no-echo rather than failure: arbitrary hosts only
// reply when something echoes UDP there.
func UDPProbe(ctx context.Context, host string, port, timeoutSec, nbytes int) (UDPResult, error) {
	host = strings.TrimSpace(host)
	if err := validateProbeTarget(host, port); err != nil {
		return UDPResult{}, err
	}
	timeoutSec = clampProbeTimeout(timeoutSec, probeMaxTimeout)
	if nbytes <= 0 {
		nbytes = udpDefaultBytes
	}
	if nbytes > udpMaxBytes {
		nbytes = udpMaxBytes
	}
	raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return UDPResult{Host: host, Port: port, Error: err.Error()}, nil
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return UDPResult{Host: host, Port: port, Error: err.Error()}, nil
	}
	defer conn.Close()
	nonce := make([]byte, nbytes)
	for i := range nonce {
		nonce[i] = byte(i % 251)
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	_ = conn.SetDeadline(deadline)
	start := time.Now()
	if _, err := conn.Write(nonce); err != nil {
		if ctx.Err() != nil {
			return UDPResult{Host: host, Port: port, Error: ctx.Err().Error()}, nil
		}
		return UDPResult{Host: host, Port: port, Error: err.Error()}, nil
	}
	buf := make([]byte, udpMaxBytes)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return UDPResult{Host: host, Port: port, Note: "no echo — expected unless peer echoes"}, nil
		}
		if !matchUDPReply(from, raddr) {
			continue
		}
		ms := time.Since(start).Milliseconds()
		return UDPResult{Host: host, Port: port, OK: true, RttMs: ms, Note: fmt.Sprintf("echoed %d bytes", n)}, nil
	}
}
