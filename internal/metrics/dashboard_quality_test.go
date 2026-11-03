package metrics

import (
	"os"
	"strings"
	"testing"
)

// Offer 1+2: quality history chart + saturation gauges, no fake cwnd.
func TestDashboardHasQualityHistory(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	for _, want := range []string{`id="spark3"`, "histRtt", "drawQuality", "loss_pct", `id="lgRtt"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(s, "innerHTML") {
		t.Fatal("dashboard must not use innerHTML (XSS)")
	}
}

func TestDashboardHasSaturationGauges(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	for _, want := range []string{`id="satQueues"`, `id="satStreams"`, `id="satTarget"`, `id="satMem"`, "renderSaturation"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestDashboardDropsFakeCwnd(t *testing.T) {
	html, _ := os.ReadFile("dashboard.html")
	s := string(html)
	if strings.Contains(s, "cwnd flat") {
		t.Fatal("hint must not reference fake cwnd (always 0 from stats.go)")
	}
	if strings.Contains(s, `cwnd "+`) {
		t.Fatal("cfgHealth must not render cwnd")
	}
}
