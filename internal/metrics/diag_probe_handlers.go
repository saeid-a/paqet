package metrics

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
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

// probeHistory is the shared last-20 probe result ring.
var probeHistory = diag.NewHistory()

// recordProbe stores one entry; label is the UI's before/after tag.
func recordProbe(kind, label, summary string, payload any) {
	probeHistory.Record(kind, label, summary, payload)
}

// diagResultsHandler serves GET /api/diag/results.
func diagResultsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entries := probeHistory.List()
	if entries == nil {
		entries = []diag.ProbeEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries, "count": len(entries)})
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
		Label      string `json:"label"`
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
	summary := "fail: " + res.Error
	if res.OK {
		summary = "ok " + strconv.FormatInt(res.ConnectMs, 10) + "ms"
	}
	recordProbe("dial", req.Label, host+":"+strconv.Itoa(req.Port)+" "+summary, res)
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
		Label      string `json:"label"`
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
	summary := "no echo"
	if res.OK {
		summary = "echo " + strconv.FormatInt(res.RttMs, 10) + "ms"
	} else if res.Error != "" {
		summary = "fail: " + res.Error
	}
	recordProbe("udp", req.Label, host+":"+strconv.Itoa(req.Port)+" "+summary, res)
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
		Host  string `json:"host"`
		Port  int    `json:"port"`
		Label string `json:"label"`
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
	summary := "iface " + strconv.Itoa(res.InterfaceMTU) + " kcp " + strconv.Itoa(res.KCPMTU)
	if res.ProbeMTU > 0 {
		summary += " probe " + strconv.Itoa(res.ProbeMTU)
	}
	recordProbe("mtu", req.Label, host+":"+strconv.Itoa(req.Port)+" "+summary, res)
	writeDiagJSON(w, res)
}

// speedtestMgr is the shared single-flight manager. Its snapshot hook
// reads live SnapshotNow counters (same package, no import cycle); the
// transfer window observes real tunnel traffic for the duration.
var speedtestMgr = diag.NewSpeedtestManager(func() (uint64, uint64, uint64, uint64, uint64) {
	snap := SnapshotNow()
	return snap.BytesIn, snap.BytesOut, snap.KCP.Retransmits, snap.KCP.LostSegs, snap.KCP.FECRecovered
})

// diagSpeedtestHandler serves POST /api/diag/speedtest.
func diagSpeedtestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		DurationSec int    `json:"duration_sec"`
		Mode        string `json:"mode"`
		Label       string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "up"
	}
	if !diag.ValidSpeedtestMode(mode) {
		http.Error(w, "unknown mode (want up|down|bidir)", http.StatusBadRequest)
		return
	}
	durationSec := req.DurationSec
	if durationSec <= 0 {
		durationSec = 10
	}
	if durationSec > 10 {
		durationSec = 10
	}
	speedtestMgr.SetActive(SnapshotNow().ActiveConnections > 0)
	v, ok := runDiagWithTimeout(r.Context(), time.Duration(durationSec+2)*time.Second, func() any {
		res, err := speedtestMgr.Run(r.Context(), durationSec, mode)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return res
	})
	if !ok {
		http.Error(w, "speedtest timed out", http.StatusGatewayTimeout)
		return
	}
	if m, isMap := v.(map[string]any); isMap {
		if msg, hasErr := m["error"]; hasErr {
			msgStr, _ := msg.(string)
			flog.Auditf("diag speedtest from %s refused: %s", remoteIP(r), msgStr)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": msgStr, "retry_after_sec": durationSec})
			return
		}
	}
	res, _ := v.(diag.SpeedtestResult)
	flog.Auditf("diag speedtest from %s mode=%s dur=%d up=%d down=%d mbps=%.2f", remoteIP(r), mode, res.DurationSec, res.BytesUp, res.BytesDown, res.GoodputMbps)
	summary := mode + " " + strconv.FormatFloat(res.GoodputMbps, 'f', 2, 64) + " Mbps up " + strconv.FormatUint(res.BytesUp, 10) + " down " + strconv.FormatUint(res.BytesDown, 10)
	recordProbe("speedtest", req.Label, summary, res)
	writeDiagJSON(w, res)
}
