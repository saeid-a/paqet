package metrics

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"paqet/internal/conf"
	"paqet/internal/diag"
	"paqet/internal/flog"
)

// Bounds for the probe lab (see spec: dial/UDP default 5s max 10s,
// speedtest default 10s max 10s + 2s grace).
const (
	probeDefaultSec = 5
	probeMaxSec     = 10
	// diagMTUSweepTimeout bounds the whole MTU sweep (plan: ≤10s total).
	diagMTUSweepTimeout = 10 * time.Second
)

// dialSem caps concurrent dial-like probes (dial + UDP share it).
// Overflow responds 429 instead of queueing against the daemon.
var dialSem = make(chan struct{}, 4)

// clampProbeSec bounds v to [1, max]; zero/negative uses def.
func clampProbeSec(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// tryAcquireProbe says whether a probe slot was available.
func tryAcquireProbe() bool {
	select {
	case dialSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseProbe() {
	select {
	case <-dialSem:
	default:
	}
}

// diagDialHandler serves POST /api/diag/dial.
func diagDialHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !tryAcquireProbe() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many probes", http.StatusTooManyRequests)
		return
	}
	defer releaseProbe()
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct {
		Host       string `json:"host"`
		Port       int    `json:"port"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(req.Host)
	if host == "" || len(host) > 253 {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		http.Error(w, "invalid port (want 1-65535)", http.StatusBadRequest)
		return
	}
	timeoutSec := clampProbeSec(req.TimeoutSec, probeDefaultSec, probeMaxSec)
	v, ok := runDiagWithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second, func() any {
		res, err := diag.DialProbe(r.Context(), host, req.Port, timeoutSec)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return res
	})
	if !ok {
		http.Error(w, "dial probe timed out", http.StatusGatewayTimeout)
		return
	}
	if m, isMap := v.(map[string]any); isMap {
		if _, hasErr := m["error"]; hasErr {
			http.Error(w, "invalid probe: "+m["error"].(string), http.StatusBadRequest)
			return
		}
	}
	res, _ := v.(diag.DialResult)
	flog.Auditf("diag dial %s:%d from %s ok=%v err=%q", host, req.Port, remoteIP(r), res.OK, res.Error)
	writeDiagJSON(w, res)
}

// diagUDPHandler serves POST /api/diag/udp.
func diagUDPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !tryAcquireProbe() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many probes", http.StatusTooManyRequests)
		return
	}
	defer releaseProbe()
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct {
		Host       string `json:"host"`
		Port       int    `json:"port"`
		TimeoutSec int    `json:"timeout_sec"`
		Bytes      int    `json:"bytes"`
		NBytes     int    `json:"nbytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(req.Host)
	if host == "" || len(host) > 253 {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		http.Error(w, "invalid port (want 1-65535)", http.StatusBadRequest)
		return
	}
	nbytes := req.Bytes
	if nbytes == 0 {
		nbytes = req.NBytes
	}
	timeoutSec := clampProbeSec(req.TimeoutSec, probeDefaultSec, probeMaxSec)
	v, ok := runDiagWithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second, func() any {
		res, err := diag.UDPProbe(r.Context(), host, req.Port, timeoutSec, nbytes)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return res
	})
	if !ok {
		http.Error(w, "udp probe timed out", http.StatusGatewayTimeout)
		return
	}
	if m, isMap := v.(map[string]any); isMap {
		if msg, hasErr := m["error"]; hasErr {
			http.Error(w, "invalid probe: "+msg.(string), http.StatusBadRequest)
			return
		}
	}
	res, _ := v.(diag.UDPResult)
	flog.Auditf("diag udp %s:%d from %s ok=%v err=%q note=%q", host, req.Port, remoteIP(r), res.OK, res.Error, res.Note)
	writeDiagJSON(w, res)
}

// probeKCPMTU reads the configured KCP MTU best-effort; 0 when the config
// is unreadable or has no KCP section.
func probeKCPMTU() int {
	path := getConfigPath()
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	cfg, err := conf.LoadFromBytes(data)
	if err != nil {
		return 0
	}
	if cfg.Transport.KCP == nil {
		return 0
	}
	return cfg.Transport.KCP.MTU
}

// diagMTUHandler serves POST /api/diag/mtu.
func diagMTUHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(req.Host)
	if host == "" || len(host) > 253 {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		http.Error(w, "invalid port (want 1-65535)", http.StatusBadRequest)
		return
	}
	v, ok := runDiagWithTimeout(r.Context(), diagMTUSweepTimeout, func() any {
		res, err := diag.MTUProbe(r.Context(), host, req.Port)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return res
	})
	if !ok {
		http.Error(w, "mtu probe timed out", http.StatusGatewayTimeout)
		return
	}
	if m, isMap := v.(map[string]any); isMap {
		if msg, hasErr := m["error"]; hasErr {
			http.Error(w, "invalid probe: "+msg.(string), http.StatusBadRequest)
			return
		}
	}
	res, _ := v.(diag.MTUResult)
	res.KCPMTU = probeKCPMTU()
	flog.Auditf("diag mtu %s:%d from %s iface=%d kcp=%d probe=%d", host, req.Port, remoteIP(r), res.InterfaceMTU, res.KCPMTU, res.ProbeMTU)
	writeDiagJSON(w, res)
}
