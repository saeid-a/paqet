package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func errUnknownListener(id string) error { return errors.New("unknown socks listener " + id) }

func setupEgressController(t *testing.T, specs map[string]string, resolved map[string]string) {
	t.Helper()
	prev := getController()
	t.Cleanup(func() { SetController(prev) })
	SetController(Controller{
		Listeners: func() []string { return []string{"socks5:127.0.0.1:1080", "forward:127.0.0.1:18081"} },
		ListenerEgressSet: func(ctx context.Context, id, egress string) error {
			if _, ok := specs[id]; !ok {
				return errUnknownListener(id)
			}
			specs[id] = egress
			return nil
		},
		ListenerEgressGet: func(id string) (string, string, bool) {
			s, ok := specs[id]
			if !ok {
				return "", "", false
			}
			return s, resolved[s], true
		},
	})
}

func TestListenerEgressSet(t *testing.T) {
	specs := map[string]string{"socks5:127.0.0.1:1080": ""}
	setupEgressController(t, specs, map[string]string{"127.0.0.1:5005": "127.0.0.1:5005"})
	req := httptest.NewRequest("POST", "/api/listeners/socks5:127.0.0.1:1080/egress", strings.NewReader(`{"egress":"127.0.0.1:5005"}`))
	rec := httptest.NewRecorder()
	listenerEgressHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["egress"] != "127.0.0.1:5005" || body["resolved"] != "127.0.0.1:5005" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestListenerEgressBadTarget(t *testing.T) {
	specs := map[string]string{}
	setupEgressController(t, specs, map[string]string{})
	for _, e := range []string{`{"egress":"garbage"}`, `{"egress":"h:99999"}`, `{"egress":"@bogus"}`} {
		req := httptest.NewRequest("POST", "/api/listeners/socks5:127.0.0.1:1080/egress", strings.NewReader(e))
		rec := httptest.NewRecorder()
		listenerEgressHandler(rec, req)
		if rec.Code != 400 {
			t.Fatalf("egress %s: status = %d, want 400", e, rec.Code)
		}
	}
}

func TestListenerEgressUnknown(t *testing.T) {
	specs := map[string]string{}
	setupEgressController(t, specs, map[string]string{})
	req := httptest.NewRequest("POST", "/api/listeners/socks5:127.0.0.1:9/egress", strings.NewReader(`{"egress":""}`))
	rec := httptest.NewRecorder()
	listenerEgressHandler(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListenerEgressUnwired(t *testing.T) {
	prev := getController()
	t.Cleanup(func() { SetController(prev) })
	SetController(Controller{})
	req := httptest.NewRequest("POST", "/api/listeners/socks5:127.0.0.1:1080/egress", strings.NewReader(`{"egress":""}`))
	rec := httptest.NewRecorder()
	listenerEgressHandler(rec, req)
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestListenersMergeEgress(t *testing.T) {
	specs := map[string]string{"socks5:127.0.0.1:1080": "@active"}
	setupEgressController(t, specs, map[string]string{"@active": "127.0.0.1:5005"})
	req := httptest.NewRequest("GET", "/api/listeners", nil)
	rec := httptest.NewRecorder()
	listenersHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, e := range body["listeners"].([]any) {
		m := e.(map[string]any)
		if m["id"] == "socks5:127.0.0.1:1080" {
			found = true
			if m["egress"] != "@active" || m["egress_target"] != "127.0.0.1:5005" {
				t.Fatalf("egress not merged: %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("socks listener missing: %v", body)
	}
}
