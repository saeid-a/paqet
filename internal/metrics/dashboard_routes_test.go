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

func TestDashboardHasConfigSafety(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    for _, want := range []string{"api/config/validate", `id="cfgDiff"`, `id="bCfgValidate"`, "AUDIT"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasDrain(t *testing.T) {
    html, err := os.ReadFile("dashboard.html")
    if err != nil {
        t.Fatal(err)
    }
    s := string(html)
    for _, want := range []string{`id="bDrain"`, "api/core/drain", "drainControl"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasRuntimeDetail(t *testing.T) {
    html, err := os.ReadFile("dashboard.html")
    if err != nil {
        t.Fatal(err)
    }
    s := string(html)
    for _, want := range []string{`id="rtDetail"`, "api/runtime", "loadRuntime"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasTargetHealth(t *testing.T) {
    html, err := os.ReadFile("dashboard.html")
    if err != nil {
        t.Fatal(err)
    }
    s := string(html)
    for _, want := range []string{`id="bConnCsv"`, "success-rate", "tgtHealth"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasAlertCfg(t *testing.T) {
    html, err := os.ReadFile("dashboard.html")
    if err != nil {
        t.Fatal(err)
    }
    s := string(html)
    for _, want := range []string{`id="alertThr"`, `id="alertMute"`, "getAlertCfg"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasTokenGen(t *testing.T) {
    html, err := os.ReadFile("dashboard.html")
    if err != nil {
        t.Fatal(err)
    }
    s := string(html)
    for _, want := range []string{`id="bGenToken"`, `id="sshSnippet"`, "genToken"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasTLSExpiry(t *testing.T) {
    html, err := os.ReadFile("dashboard.html")
    if err != nil {
        t.Fatal(err)
    }
    s := string(html)
    for _, want := range []string{"tls_expiry", "tls:off", "tls:on"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasAuditTimeline(t *testing.T) {
    html, err := os.ReadFile("dashboard.html")
    if err != nil {
        t.Fatal(err)
    }
    s := string(html)
    for _, want := range []string{`id="auditList"`, "loadAudit", `id="bAuditReload"`} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasTabDeepLink(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    for _, want := range []string{"hashchange", "location.hash", "tabFromHash"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}

func TestDashboardHasSecondSparkline(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    for _, want := range []string{`id="spark2"`, "histConn", "drawConn"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
}
