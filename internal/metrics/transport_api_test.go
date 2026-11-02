package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusIncludesTransport(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(rec, req)
	if !strings.Contains(rec.Body.String(), "snd_queue") {
		t.Fatalf("status missing transport.snd_queue")
	}
}

func TestStatusIncludesKCPDetail(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(rec, req)
	body := rec.Body.String()
	for _, k := range []string{"srtt_var_ms", "lost_segs", "fec_recovered", "target_fill"} {
		if !strings.Contains(body, k) {
			t.Fatalf("status missing %s", k)
		}
	}
}
