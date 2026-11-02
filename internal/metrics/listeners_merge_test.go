package metrics

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListenersMergeConfigMeta(t *testing.T) {
	yaml := `role: "client"
server:
  addr: "127.0.0.1:9999"
network:
  interface: "en0"
  ipv4:
    addr: "127.0.0.1:0"
    router_mac: "aa:bb:cc:dd:ee:ff"
socks5:
  - listen: "127.0.0.1:18080"
forward:
  - listen: "127.0.0.1:18081"
    target: "127.0.0.1:9"
    protocol: "tcp"
transport:
  protocol: "kcp"
  kcp:
    block: "none"
`
	path := filepath.Join(t.TempDir(), "client.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	prevPath, prevCtl := getConfigPath(), getController()
	defer func() {
		SetConfigPath(prevPath)
		SetController(prevCtl)
	}()
	SetConfigPath(path)
	SetController(Controller{
		Listeners:       func() []string { return []string{"forward:127.0.0.1:18081", "custom:thing"} },
		ListenerRunning: func(id string) bool { return true },
	})

	req := httptest.NewRequest("GET", "/api/listeners", nil)
	rec := httptest.NewRecorder()
	listenersHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Listeners []map[string]any `json:"listeners"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, e := range body.Listeners {
		if id, _ := e["id"].(string); id != "" {
			byID[id] = e
		}
	}
	fwd, ok := byID["forward:127.0.0.1:18081"]
	if !ok {
		t.Fatalf("missing forward entry: %v", body.Listeners)
	}
	if fwd["type"] != "forward" || fwd["target"] != "127.0.0.1:9" || fwd["protocol"] != "tcp" {
		t.Errorf("forward entry missing merged meta: %v", fwd)
	}
	if fwd["running"] != true {
		t.Errorf("forward entry missing running state: %v", fwd)
	}
	custom, ok := byID["custom:thing"]
	if !ok {
		t.Fatalf("custom entry dropped: %v", body.Listeners)
	}
	if custom["running"] != true {
		t.Errorf("custom entry missing running state: %v", custom)
	}
	if _, has := custom["target"]; has {
		t.Errorf("custom entry gained spurious target: %v", custom)
	}
}
