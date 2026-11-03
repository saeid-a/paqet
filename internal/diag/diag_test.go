package diag

import (
	"strings"
	"testing"
)

// RED: port 80 must not match a rule for 8080.
func TestFirewallPortBoundary(t *testing.T) {
	combined := strings.Join([]string{
		"-A PREROUTING -p tcp --dport 8080 -j NOTRACK",
		"-A OUTPUT -p tcp --sport 8080 -j NOTRACK",
		"-A OUTPUT -p tcp --sport 8080 --tcp-flags RST RST -j DROP",
	}, "\n")
	hasDport, hasSport, hasRST := firewallRulesPresent(combined, 80)
	if hasDport || hasSport || hasRST {
		t.Fatalf("port 80 must not match 8080 rules, got dport=%v sport=%v rst=%v", hasDport, hasSport, hasRST)
	}
	hasDport, hasSport, hasRST = firewallRulesPresent(combined, 8080)
	if !hasDport || !hasSport || !hasRST {
		t.Fatalf("port 8080 must match its own rules, got dport=%v sport=%v rst=%v", hasDport, hasSport, hasRST)
	}
}

func TestClientFirewallFixScopedToServer(t *testing.T) {
	fix := ClientFirewallFix("10.0.0.100", 9999)
	joined := strings.Join(fix, "\n")
	if !strings.Contains(joined, "10.0.0.100") {
		t.Fatalf("fix must scope to server IP, got %q", joined)
	}
	if !strings.Contains(joined, "9999") {
		t.Fatalf("fix must contain port, got %q", joined)
	}
}
