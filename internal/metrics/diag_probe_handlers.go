package metrics

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"paqet/internal/diag"
	"paqet/internal/flog"
)

// Bounds for the probe lab (see spec: dial/UDP default 5s max 10s,
// speedtest default 10s max 10s + 2s grace).
const (
	probeDefaultSec = 5
	probeMaxSec     = 10
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
