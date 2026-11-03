package metrics

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigValidateRejectsBadYAMLWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfg, []byte(fixTestYAML), 0600)
	SetConfigPath(cfg)
	before, _ := os.ReadFile(cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/config/validate", strings.NewReader(`{"yaml":"role: bogus"}`))
	rec := httptest.NewRecorder()
	configValidateHandler(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rec.Code)
	}
	after, _ := os.ReadFile(cfg)
	if string(before) != string(after) {
		t.Fatal("validate must not write disk")
	}
}

func TestConfigValidateAcceptsGoodYAML(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"yaml": fixTestYAML})
	req := httptest.NewRequest(http.MethodPost, "/api/config/validate", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	configValidateHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("want ok:true, got %s", rec.Body.String())
	}
}

func TestConfigValidateRejectsOversize(t *testing.T) {
	big := strings.Repeat("x", (1<<20)+1)
	payload, _ := json.Marshal(map[string]string{"yaml": big})
	req := httptest.NewRequest(http.MethodPost, "/api/config/validate", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	configValidateHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestConfigValidateSetsNoStore(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   string
	}{
		{"ok", http.MethodPost, `{"yaml":"role: bogus"}`}, // 422 path
		{"bad-json", http.MethodPost, `not-json`},         // 400 path
		{"wrong-method", http.MethodGet, ``},              // 405 path
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body == "" {
				req = httptest.NewRequest(tc.method, "/api/config/validate", nil)
			} else {
				req = httptest.NewRequest(tc.method, "/api/config/validate", strings.NewReader(tc.body))
			}
			rec := httptest.NewRecorder()
			configValidateHandler(rec, req)
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("want Cache-Control no-store, got %q", got)
			}
		})
	}
	// 200 path
	payload, _ := json.Marshal(map[string]string{"yaml": fixTestYAML})
	req := httptest.NewRequest(http.MethodPost, "/api/config/validate", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	configValidateHandler(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("want Cache-Control no-store on 200, got %q", got)
	}
}
