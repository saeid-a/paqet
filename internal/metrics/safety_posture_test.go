package metrics

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPostureTLSExpiry(t *testing.T) {
	// valid cert: generate a self-signed cert, expect TLSExpiry parses as RFC3339
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}
	SetTLS(certPath, keyPath)
	defer SetTLS("", "")
	SetAdminToken("")
	defer SetAdminToken("")
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
	if !p.TLSEnabled {
		t.Fatal("want tls_enabled=true")
	}
	if p.TLSExpiry == "" {
		t.Fatal("want tls_expiry non-empty")
	}
	if _, err := time.Parse(time.RFC3339, p.TLSExpiry); err != nil {
		t.Fatalf("tls_expiry not RFC3339: %q (%v)", p.TLSExpiry, err)
	}

	// bogus cert path: expect TLSError non-empty, TLSEnabled true (paths set), no panic
	SetTLS(filepath.Join(dir, "missing.pem"), keyPath)
	req2 := httptest.NewRequest(http.MethodGet, "/api/safety/posture", nil)
	req2.RemoteAddr = "127.0.0.1:1"
	rec2 := httptest.NewRecorder()
	postureHandler(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec2.Code)
	}
	var p2 PostureResponse
	if err := json.NewDecoder(rec2.Body).Decode(&p2); err != nil {
		t.Fatal(err)
	}
	if !p2.TLSEnabled {
		t.Fatal("want tls_enabled=true with paths set")
	}
	if p2.TLSError == "" {
		t.Fatal("want tls_error non-empty for bogus cert path")
	}
}
