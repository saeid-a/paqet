package metrics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"paqet/internal/diag"
)

// Timeouts bounding the diag/net admin endpoints. The HTTP server already
// enforces read/write deadlines; these bound the diagnostic work itself.
const (
	diagInterfacesTimeout = 5 * time.Second
	diagFirewallTimeout   = 10 * time.Second
	diagDumpTimeout       = 10 * time.Second
	diagPingDefaultSec    = 10
	diagPingMaxSec        = 10
)

// runDiagWithTimeout runs fn and reports false if it exceeds timeout.
// It derives from parent (usually r.Context()) so client disconnect aborts
// the wait instead of holding the handler up to timeout.
func runDiagWithTimeout(parent context.Context, timeout time.Duration, fn func() any) (any, bool) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ch := make(chan any, 1)
	go func() { ch <- fn() }()
	select {
	case v := <-ch:
		return v, true
	case <-ctx.Done():
		return nil, false
	}
}

func writeDiagJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// interfacesHandler serves GET /api/net/interfaces.
func interfacesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	v, ok := runDiagWithTimeout(r.Context(), diagInterfacesTimeout, func() any {
		ifaces, err := diag.Interfaces()
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		if ifaces == nil {
			ifaces = []diag.InterfaceInfo{}
		}
		return map[string]any{"interfaces": ifaces, "count": len(ifaces)}
	})
	if !ok {
		http.Error(w, "interfaces lookup timed out", http.StatusGatewayTimeout)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if m, isMap := v.(map[string]any); isMap {
		if _, hasErr := m["error"]; hasErr {
			w.WriteHeader(http.StatusInternalServerError)
			if r.Method == http.MethodHead {
				return
			}
			_ = json.NewEncoder(w).Encode(v)
			return
		}
	}
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// firewallHandler serves GET /api/net/firewall?port=9999.
func firewallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	port := 9999
	if s := strings.TrimSpace(r.URL.Query().Get("port")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 65535 {
			http.Error(w, "invalid port (want 1-65535)", http.StatusBadRequest)
			return
		}
		port = n
	}
	v, ok := runDiagWithTimeout(r.Context(), diagFirewallTimeout, func() any {
		return diag.FirewallCheck(port)
	})
	if !ok {
		http.Error(w, "firewall check timed out", http.StatusGatewayTimeout)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		return
	}
	writeDiagJSON(w, v)
}

// diagPingHandler serves POST /api/diag/ping.
func diagPingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct {
		ConfigPath string `json:"config_path"`
		Config     string `json:"config"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	cfgPath := strings.TrimSpace(req.ConfigPath)
	if cfgPath == "" {
		cfgPath = strings.TrimSpace(req.Config)
	}
	if cfgPath == "" {
		cfgPath = getConfigPath()
	}
	timeoutSec := req.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = diagPingDefaultSec
	}
	if timeoutSec > diagPingMaxSec {
		timeoutSec = diagPingMaxSec
	}
	v, ok := runDiagWithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second, func() any {
		return diag.PingSummary(cfgPath, timeoutSec)
	})
	if !ok {
		http.Error(w, "ping diagnostic timed out", http.StatusGatewayTimeout)
		return
	}
	res, _ := v.(diag.PingResult)
	if res.Error == "not wired" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "ping diagnostic not wired to an implementation"})
		return
	}
	writeDiagJSON(w, map[string]any{
		"config_path": cfgPath,
		"timeout_sec": timeoutSec,
		"error":       res.Error,
	})
}

// diagDumpHandler serves POST /api/diag/dump.
func diagDumpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Port  int `json:"port"`
		Count int `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	port := req.Port
	if port == 0 {
		port = 9999
	}
	if port < 1 || port > 65535 {
		http.Error(w, "invalid port (want 1-65535)", http.StatusBadRequest)
		return
	}
	count := req.Count
	if count <= 0 {
		count = 10
	}
	if count > 1000 {
		count = 1000
	}
	v, ok := runDiagWithTimeout(r.Context(), diagDumpTimeout, func() any {
		return diag.DumpCapture(port, count)
	})
	if !ok {
		http.Error(w, "dump diagnostic timed out", http.StatusGatewayTimeout)
		return
	}
	res, _ := v.(diag.DumpResult)
	if res.Error == "not wired" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "dump diagnostic not wired to an implementation"})
		return
	}
	writeDiagJSON(w, map[string]any{
		"port":  port,
		"count": count,
		"error": res.Error,
	})
}
