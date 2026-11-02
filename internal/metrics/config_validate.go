package metrics

import (
	"encoding/json"
	"net/http"
	"strings"

	"paqet/internal/conf"
)

// validateResponse is the dry-run validation result. The token value is
// never included; only ok/errors are returned.
type validateResponse struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}

// splitValidationErrors splits a joined conf validation error
// ("validation failed:\n  - ...\n  - ...") into individual messages.
// Single-line errors (e.g. YAML syntax or role errors) yield one entry.
func splitValidationErrors(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		if line == "" || line == "validation failed:" {
			continue
		}
		out = append(out, line)
	}
	if out == nil {
		out = []string{strings.TrimSpace(err.Error())}
	}
	return out
}

// configValidateHandler dry-runs YAML validation without touching disk.
// It never calls backupConfigFile or os.WriteFile.
func configValidateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.YAML) == "" {
		http.Error(w, "empty config", http.StatusBadRequest)
		return
	}
	if _, err := conf.LoadFromBytes([]byte(req.YAML)); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(validateResponse{OK: false, Errors: splitValidationErrors(err)})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(validateResponse{OK: true})
}
