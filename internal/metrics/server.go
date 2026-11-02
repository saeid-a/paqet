package metrics

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"paqet/internal/conf"
	"paqet/internal/flog"
)

// Controller wires the web UI to the core process lifecycle.
// Start/Stop/Restart are provided by cmd/run (supervisor).
type Controller struct {
	ConfigPath string
	Running    func() bool
	Start      func() error
	Stop       func() error
	Restart    func() error
}

var (
	ctrl       atomic.Value // Controller
	running    atomic.Bool
	configPath atomic.Value // string
	buildInfo  atomic.Value // BuildInfo
)

type BuildInfo struct {
	Version   string `json:"version"`
	GitTag    string `json:"git_tag"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	Platform  string `json:"platform"`
}

func SetController(c Controller) { ctrl.Store(c) }
func SetRunning(v bool)          { running.Store(v) }
func SetConfigPath(p string)     { configPath.Store(p) }
func SetBuildInfo(b BuildInfo)   { buildInfo.Store(b) }

func getController() Controller {
	if v, ok := ctrl.Load().(Controller); ok {
		return v
	}
	return Controller{}
}

func getConfigPath() string {
	if v, ok := configPath.Load().(string); ok {
		return v
	}
	return ""
}

func getBuildInfo() BuildInfo {
	if v, ok := buildInfo.Load().(BuildInfo); ok {
		return v
	}
	return BuildInfo{}
}

func Start(ctx context.Context, cfg conf.Metrics) {
	c := getController()
	if c.ConfigPath != "" {
		// controller already has path; prefer explicit cfg
		_ = c
	}
	StartWithController(ctx, cfg, getController())
}

func StartWithController(ctx context.Context, cfg conf.Metrics, c Controller) {
	if !cfg.Enabled {
		return
	}
	SetController(c)
	if c.ConfigPath != "" {
		SetConfigPath(c.ConfigPath)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", Handler)
	mux.HandleFunc("/api/status", statusHandler)
	mux.HandleFunc("/api/config", configHandler)
	mux.HandleFunc("/api/control", controlHandler)
	mux.HandleFunc("/api/logs", logsHandler)
	mux.HandleFunc("/api/version", versionHandler)
	mux.HandleFunc("/", dashboardHandler)

	listener, err := net.Listen("tcp", cfg.Listen_)
	if err != nil {
		flog.Errorf("admin listener failed on %s: %v", cfg.Listen_, err)
		return
	}

	server := &http.Server{
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shCtx)
	}()

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			flog.Errorf("admin server error: %v", err)
		}
	}()

	flog.Infof("control panel enabled on http://%s", cfg.Listen_)
}

type statusResponse struct {
	Snapshot
	Running    bool      `json:"running"`
	ConfigPath string    `json:"config_path"`
	Build      BuildInfo `json:"build"`
}

func getRunning() bool {
	if c := getController(); c.Running != nil {
		return c.Running()
	}
	return running.Load()
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 200
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}
	snap := SnapshotNow()
	if len(snap.Connections) > limit {
		snap.Connections = snap.Connections[:limit]
	}
	resp := statusResponse{
		Snapshot:   snap,
		Running:    getRunning(),
		ConfigPath: getConfigPath(),
		Build:      getBuildInfo(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

type configResponse struct {
	Path string `json:"path"`
	YAML string `json:"yaml"`
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	path := getConfigPath()
	switch r.Method {
	case http.MethodGet:
		data := ""
		if path != "" {
			if b, err := os.ReadFile(path); err == nil {
				data = string(b)
			} else {
				http.Error(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(configResponse{Path: path, YAML: data})
	case http.MethodPut:
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if path == "" {
			http.Error(w, "config path unknown, cannot save", http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(path, []byte(req.YAML), 0600); err != nil {
			http.Error(w, "cannot write config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		flog.Infof("config updated via control panel (%s)", path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved", "message": "config saved - restart to apply"})
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func controlHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	c := getController()
	var err error
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "start":
		if c.Start != nil {
			err = c.Start()
		} else {
			err = errNoController()
		}
	case "stop":
		if c.Stop != nil {
			err = c.Stop()
		} else {
			err = errNoController()
		}
	case "restart":
		if c.Restart != nil {
			err = c.Restart()
		} else {
			err = errNoController()
		}
	default:
		http.Error(w, "unknown action (want start|stop|restart)", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "running": strconv.FormatBool(getRunning())})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": req.Action, "running": strconv.FormatBool(getRunning())})
}

func errNoController() error {
	return errString("lifecycle controller not wired")
}

type errString string

func (e errString) Error() string { return string(e) }

func logsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tail := 200
	if s := r.URL.Query().Get("tail"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			tail = n
		}
	}
	if tail < 1 {
		tail = 50
	}
	if tail > 1000 {
		tail = 1000
	}
	level := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("level")))
	entries := flog.Logs(tail * 2) // fetch extra, filter then trim
	if level != "" && level != "all" {
		filtered := entries[:0]
		// in-place filter keeps allocs low
		for _, e := range entries {
			if strings.ToLower(e.Level) == level {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}
	if len(entries) > tail {
		entries = entries[len(entries)-tail:]
	}
	if entries == nil {
		entries = []flog.LogEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"logs": entries, "count": len(entries)})
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(getBuildInfo())
}
