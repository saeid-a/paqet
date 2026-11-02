package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupTunnelController(t *testing.T, fn func(target string, timeoutSec int) (int64, bool, error)) {
	t.Helper()
	prev := getController()
	t.Cleanup(func() { SetController(prev) })
	if fn == nil {
		SetController(Controller{})
		return
	}
	SetController(Controller{
		TunnelProbe: func(ctx context.Context, target string, timeoutSec int) (int64, bool, error) {
			return fn(target, timeoutSec)
		},
	})
}

func TestDiagTunnelSuccess(t *testing.T) {
	setupTunnelController(t, func(target string, timeoutSec int) (int64, bool, error) {
		if target != "127.0.0.1:9" {
			t.Errorf("target = %q", target)
		}
		if timeoutSec != 5 {
			t.Errorf("timeoutSec = %d", timeoutSec)
		}
		return 12, true, nil
	})
	req := httptest.NewRequest("POST", "/api/diag/tunnel", strings.NewReader(`{"target":"127.0.0.1:9","timeout_sec":5,"label":"forward x"}`))
	rec := httptest.NewRecorder()
	diagTunnelHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true || body["target"] != "127.0.0.1:9" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestDiagTunnelUnavailable(t *testing.T) {
	setupTunnelController(t, nil)
	req := httptest.NewRequest("POST", "/api/diag/tunnel", strings.NewReader(`{"target":"127.0.0.1:9"}`))
	rec := httptest.NewRecorder()
	diagTunnelHandler(rec, req)
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestDiagTunnelBadTarget(t *testing.T) {
	setupTunnelController(t, func(string, int) (int64, bool, error) { return 0, false, nil })
	for _, body := range []string{`{"target":"garbage"}`, `{"target":"h:99999"}`, `{"target":""}`} {
		req := httptest.NewRequest("POST", "/api/diag/tunnel", strings.NewReader(body))
		rec := httptest.NewRecorder()
		diagTunnelHandler(rec, req)
		if rec.Code != 400 {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestDiagTunnelProbeError(t *testing.T) {
	setupTunnelController(t, func(string, int) (int64, bool, error) {
		return 0, false, errors.New("target unreachable via tunnel")
	})
	req := httptest.NewRequest("POST", "/api/diag/tunnel", strings.NewReader(`{"target":"127.0.0.1:9"}`))
	rec := httptest.NewRecorder()
	diagTunnelHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != false {
		t.Fatalf("want ok=false, body: %v", body)
	}
}
