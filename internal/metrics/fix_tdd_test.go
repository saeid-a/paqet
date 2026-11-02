package metrics

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupsPostRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(fixTestYAML), 0600); err != nil {
		t.Fatal(err)
	}
	SetConfigPath(cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/config/backups", strings.NewReader(`{"action":`))
	rec := httptest.NewRecorder()
	configBackupsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDrainRejectsInvalidJSON(t *testing.T) {
	SetController(Controller{Drain: func(timeoutSec int) error { return nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/core/drain", strings.NewReader(`{"timeout_sec":`))
	rec := httptest.NewRecorder()
	coreDrainHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestQueryTokenOnlyForStream(t *testing.T) {
	SetAdminToken("long-secret-token-12345")
	defer SetAdminToken("")
	// Header works everywhere.
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer long-secret-token-12345")
	if !isAuthorized(req) {
		t.Fatal("header token should authorize /api/status")
	}
	// Query token must NOT authorize non-SSE endpoints.
	req2 := httptest.NewRequest(http.MethodGet, "/api/status?token=long-secret-token-12345", nil)
	if isAuthorized(req2) {
		t.Fatal("query token must not authorize /api/status")
	}
	// Query token MUST still authorize SSE (EventSource cannot send headers).
	req3 := httptest.NewRequest(http.MethodGet, "/api/logs/stream?token=long-secret-token-12345", nil)
	if !isAuthorized(req3) {
		t.Fatal("query token should authorize /api/logs/stream")
	}
}
