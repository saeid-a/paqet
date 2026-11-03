package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"paqet/internal/conf"
)

// RED: stale admin token should be cleared when new config has no token.
func TestStartWithControllerClearsStaleToken(t *testing.T) {
	SetAdminToken("long-secret-token-12345")
	if got := getAdminToken(); got == "" {
		t.Fatal("setup failed: token not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := conf.Admin{Enabled: true, Listen_: "127.0.0.1:0"}
	StartWithController(ctx, cfg, Controller{})
	// allow server goroutine to start; token clearing is synchronous before listen
	if got := getAdminToken(); got != "" {
		t.Fatalf("stale token not cleared, got %q want empty", got)
	}
	// cleanup for other tests
	SetAdminToken("")
}

// RED: control endpoint should return running as bool, not string.
func TestControlRunningIsBool(t *testing.T) {
	SetController(Controller{
		Start: func() error { return nil },
		Stop:  func() error { return nil },
		Running: func() bool { return true },
	})
	req := httptest.NewRequest(http.MethodPost, "/api/control", strings.NewReader(`{"action":"start"}`))
	rec := httptest.NewRecorder()
	controlHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	v, ok := body["running"]
	if !ok {
		t.Fatalf("missing running field in %v", body)
	}
	if _, isBool := v.(bool); !isBool {
		t.Fatalf("running should be bool, got %T (%v)", v, v)
	}
}
