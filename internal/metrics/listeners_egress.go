package metrics

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"paqet/internal/conf"
	"paqet/internal/flog"
)

// listenerEgressHandler serves POST /api/listeners/{id}/egress: steer a
// SOCKS listener's egress at runtime ("", "@active", "forward:<listen>",
// or "host:port"). Runtime-only until Saved + Restarted, like the forward
// Use switch; the boot value comes from the socks5 `egress:` YAML field.
func listenerEgressHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c := getController()
	if c.ListenerEgressSet == nil || c.ListenerEgressGet == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "listener egress not implemented"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/listeners/")
	rest = strings.Trim(rest, "/")
	if !strings.HasSuffix(rest, "/egress") {
		http.Error(w, "want POST /api/listeners/{id}/egress", http.StatusBadRequest)
		return
	}
	id := strings.TrimSuffix(rest[:len(rest)-len("/egress")], "/")
	if id == "" {
		http.Error(w, "want POST /api/listeners/{id}/egress", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Egress string `json:"egress"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	spec := strings.TrimSpace(req.Egress)
	if err := conf.ValidateSocksEgress(spec); err != nil {
		http.Error(w, "invalid egress: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := c.ListenerEgressSet(r.Context(), id, spec); err != nil {
		flog.Auditf("listener %s egress %q from %s err=%v", id, spec, remoteIP(r), err)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(err.Error(), "unknown") {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	resolved := ""
	if s, res, ok := c.ListenerEgressGet(id); ok {
		_ = s
		resolved = res
	}
	flog.Auditf("listener %s egress %q (resolved %q) from %s", id, spec, resolved, remoteIP(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": id, "egress": spec, "resolved": resolved})
}
