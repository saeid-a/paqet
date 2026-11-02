package metrics

import (
	"os"
	"strings"
	"testing"
)

// Dashboard firewall button must call the route the server registers.
func TestDashboardFirewallPathMatchesServer(t *testing.T) {
	html, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if strings.Contains(s, "api/diag/firewall") {
		t.Fatalf("dashboard calls api/diag/firewall, server registers /api/net/firewall")
	}
	if !strings.Contains(s, "api/net/firewall") {
		t.Fatalf("dashboard missing api/net/firewall call")
	}
}
