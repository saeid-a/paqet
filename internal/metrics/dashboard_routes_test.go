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

func TestDashboardHasPostureCard(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	if !strings.Contains(s, "api/safety/posture") { t.Fatal("missing posture fetch") }
	if !strings.Contains(s, `id="postureCard"`) { t.Fatal("missing #postureCard") }
	if !strings.Contains(s, `id="exposureBanner"`) { t.Fatal("missing #exposureBanner") }
}

func TestDashboardHasSetupTab(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	for _, want := range []string{`data-t="setup"`, `id="t-setup"`, "api/safety/preflight", "copyText"} {
		if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
	}
}
