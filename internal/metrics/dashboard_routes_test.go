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

func TestDashboardHasKcpDetail(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    for _, want := range []string{`id="kcpVar"`, `id="kcpSegs"`, `id="kcpFecD"`, "renderKcpDetail", "srtt_var_ms", "fast_retrans_segs", "fmtInt"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
    if !strings.Contains(s, "kcpVar") || !strings.Contains(s, "—") { t.Fatal("missing zero-guard fallback") }
}

func TestDashboardHasTransportPressure(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    for _, want := range []string{`id="trQueues"`, `id="trHint"`, "renderTransport", "snd_queue", "target_fill", "no data"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
    if strings.Contains(s, "innerHTML") { t.Fatal("dashboard must not use innerHTML (XSS)") }
}

func TestDashboardHasConnHistory(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    for _, want := range []string{`id="modalHist"`, "trackConnHist", "connHist", "closedAt", "textContent"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
    if strings.Contains(s, "innerHTML") { t.Fatal("dashboard must not use innerHTML (XSS)") }
    if !strings.Contains(s, "(connGone||[])") { t.Fatal("trackConnHist must retain tombstones via connGone") }
}

func TestDashboardTransportHintGuardsMissingQueues(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    if !strings.Contains(s, "sq!=null&&rq!=null") { t.Fatal("renderTransport must guard missing snd_queue/rcv_queue before claiming queues clear") }
}

func TestDashboardHasProbeLab(t *testing.T) {
    html, _ := os.ReadFile("dashboard.html")
    s := string(html)
    for _, want := range []string{`id="probeHost"`, `id="probePort"`, `id="probeResults"`, `id="probeHistory"`, "runProbe", "api/diag/dial", "api/diag/speedtest", "api/diag/results", "textContent"} {
        if !strings.Contains(s, want) { t.Fatalf("missing %q", want) }
    }
    if strings.Contains(s, "innerHTML") { t.Fatal("probe path must not use innerHTML (XSS)") }
    if !strings.Contains(s, `kind!=="speedtest"`) { t.Fatal("runProbe must skip host/port gate for speedtest (no target param)") }
    if !strings.Contains(s, "observed") { t.Fatal("probe lab must label speedtest as observed traffic (idle tunnel reads ~0)") }
}

func TestDashboardForwardTestButton(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	// Buttons are created via setAttribute in inline JS, so assert on the
	// call form rather than literal HTML attributes. The test runs through
	// the tunnel, not a direct dial: forward targets are dialed by the server.
	for _, want := range []string{"listenerTest", "api/diag/tunnel", `"data-testid"`, `"fwd-test"`, `"fwd-test-result"`, "textContent", "through the tunnel", "reachable (silent)"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(s, "innerHTML") {
		t.Fatal("probe path must not use innerHTML (XSS)")
	}
}

func TestDashboardForwardSelect(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	// Use button is created via setAttribute in inline JS; the banner is
	// static HTML, so both the call form and the literal form are pinned.
	for _, want := range []string{"listenerUse", `"fwd-use"`, `"fwd-active"`, "paqet-active-forward", "runtime-only", "confirm("} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(s, "innerHTML") {
		t.Fatal("select path must not use innerHTML (XSS)")
	}
}

func TestDashboardListenersLiveInConfig(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	// The Listeners panel (with the forward Test/Use workflow) lives in the
	// Config tab, not Targets: the table markup must sit inside t-config.
	cfg := strings.Index(s, `id="t-config"`)
	tbl := strings.Index(s, `id="lstBody"`)
	if cfg < 0 || tbl < 0 || tbl < cfg {
		t.Fatalf("Listeners table not inside Config tab (cfg=%d tbl=%d)", cfg, tbl)
	}
	// Targets tab renders targets only; it must not trigger a listener load.
	if !strings.Contains(s, `if(t==="targets"){if(lastStatus)renderTargets(lastStatus);}`) {
		t.Fatal("targets tab should render targets without loading listeners")
	}
	if !strings.Contains(s, `if(t==="config"){loadCfg(false);loadListeners();}`) {
		t.Fatal("config tab should load config and listeners")
	}
}
