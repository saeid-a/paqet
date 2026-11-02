package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightListsExpectedChecks(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfg, []byte(fixTestYAML), 0600)
	SetConfigPath(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/safety/preflight", nil)
	rec := httptest.NewRecorder()
	preflightHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct{ Checks []Check }
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Checks) < 6 {
		t.Fatalf("want >=6 checks, got %d", len(body.Checks))
	}
}
