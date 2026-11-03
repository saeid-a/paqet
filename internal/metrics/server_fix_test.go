package metrics

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repro for review findings: restore validation + backups POST.
var fixTestYAML = "role: \"client\"\nlog:\n  level: \"info\"\nadmin:\n  enabled: false\nsocks5:\n  - listen: \"127.0.0.1:18081\"\nnetwork:\n  interface: \"lo0\"\n  ipv4:\n    addr: \"127.0.0.1:0\"\n    router_mac: \"aa:bb:cc:dd:ee:ff\"\nserver:\n  addr: \"127.0.0.1:9999\"\ntransport:\n  protocol: \"kcp\"\n  conn: 1\n  kcp:\n    mode: \"fast\"\n    key: \"test-secret-key-123\"\n"

func TestRestoreRejectsNonBackupName(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(fixTestYAML), 0600); err != nil {
		t.Fatal(err)
	}
	SetConfigPath(cfg)

	// evil.bak.foo lives in config dir and contains ".bak." but is not
	// a backup of config.yaml. Must be rejected as "not a backup".
	evil := filepath.Join(dir, "evil.bak.foo")
	if err := os.WriteFile(evil, []byte(fixTestYAML), 0600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/config/restore", strings.NewReader(`{"file":"evil.bak.foo"}`))
	rec := httptest.NewRecorder()
	configRestoreHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a backup") {
		t.Fatalf("want 'not a backup', got %s", rec.Body.String())
	}
}

func TestBackupsPostCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(fixTestYAML), 0600); err != nil {
		t.Fatal(err)
	}
	SetConfigPath(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/config/backups", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	configBackupsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for POST backups save, got %d body=%s", rec.Code, rec.Body.String())
	}
	matches, _ := filepath.Glob(cfg + ".bak.*")
	if len(matches) == 0 {
		t.Fatal("want backup file created, got none")
	}
}
