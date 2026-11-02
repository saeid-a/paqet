package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostureCriticalWhenExposedWithoutToken(t *testing.T) {
	SetAdminToken("")
	SetTLS("", "")
	SetConfigPath("")
	req := httptest.NewRequest(http.MethodGet, "/api/safety/posture", nil)
	req.RemoteAddr = "192.168.1.5:1234"
	rec := httptest.NewRecorder()
	postureHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var p PostureResponse
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Exposure != "critical" {
		t.Fatalf("want critical, got %q", p.Exposure)
	}
}

func TestPostureSafeWhenLoopbackWithToken(t *testing.T) {
	const token = "long-secret-token-12345"
	SetAdminToken(token)
	defer SetAdminToken("")
	SetTLS("", "")
	defer SetTLS("", "")
	SetConfigPath("")
	req := httptest.NewRequest(http.MethodGet, "/api/safety/posture", nil)
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	postureHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatal("response must not leak token value")
	}
	var p PostureResponse
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Exposure != "safe" {
		t.Fatalf("want safe, got %q", p.Exposure)
	}
	if !p.TokenSet {
		t.Fatal("want token_set=true")
	}
	if !p.IsLoopback {
		t.Fatal("want is_loopback=true for fallback bind")
	}
}

func TestPostureSafeWhenLoopbackWithTLS(t *testing.T) {
	SetAdminToken("")
	defer SetAdminToken("")
	SetTLS("cert.pem", "key.pem")
	defer SetTLS("", "")
	SetConfigPath("")
	req := httptest.NewRequest(http.MethodGet, "/api/safety/posture", nil)
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	postureHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var p PostureResponse
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Exposure != "safe" {
		t.Fatalf("want safe, got %q", p.Exposure)
	}
	if !p.TLSEnabled {
		t.Fatal("want tls_enabled=true")
	}
}

func TestPostureHeadHasNoBody(t *testing.T) {
	SetAdminToken("")
	SetTLS("", "")
	SetConfigPath("")
	req := httptest.NewRequest(http.MethodHead, "/api/safety/posture", nil)
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	postureHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("want empty body, got %q", rec.Body.String())
	}
}
