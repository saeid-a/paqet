package metrics

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	Token      string
	TLSCert    string
	TLSKey     string
	// Optional hooks for listener control and core drain.
	// If nil, the corresponding endpoint returns 501.
	Listeners       func() []string
	ListenerStart   func(id string) error
	ListenerStop    func(id string) error
	ListenerRunning func(id string) bool
	Drain           func(timeoutSec int) error
}

var (
	ctrl       atomic.Value // Controller
	running    atomic.Bool
	configPath atomic.Value // string
	buildInfo  atomic.Value // BuildInfo
	adminToken atomic.Value // string
	tlsCert    atomic.Value // string
	tlsKey     atomic.Value // string
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
func SetAdminToken(t string)     { adminToken.Store(t) }
func SetTLS(cert, key string) {
	tlsCert.Store(cert)
	tlsKey.Store(key)
}

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

func getAdminToken() string {
	if v, ok := adminToken.Load().(string); ok {
		return v
	}
	return ""
}

func getTLSCert() string {
	if v, ok := tlsCert.Load().(string); ok {
		return v
	}
	return ""
}

func getTLSKey() string {
	if v, ok := tlsKey.Load().(string); ok {
		return v
	}
	return ""
}

// isAuthorized reports whether the request carries a valid bearer token.
// When no token is configured, only loopback requests are authorized;
// remote requests without a token are denied.
func isAuthorized(r *http.Request) bool {
	tok := getAdminToken()
	if tok == "" {
		host := remoteIP(r)
		if ip := net.ParseIP(host); ip != nil {
			return ip.IsLoopback()
		}
		return host == "localhost"
	}
	if h := r.Header.Get("Authorization"); h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return false
		}
		got := strings.TrimSpace(parts[1])
		return subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1
	}
	// Fallback for EventSource, which cannot set request headers:
	// accept ?token= on the SSE endpoint only. Other endpoints must use
	// the header; allowing query tokens everywhere would leak secrets
	// into access logs and browser history.
	if q := r.URL.Query().Get("token"); q != "" {
		if r.URL.Path != "/api/logs/stream" {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(q), []byte(tok)) == 1
	}
	return false
}

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthorized(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func remoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	// Intentionally ignore X-Forwarded-For: audit entries must not be
	// attacker-controlled. Deployments behind a reverse proxy terminate
	// there; r.RemoteAddr is the trustworthy local peer.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Start keeps backward-compat for callers passing legacy Metrics.
// It converts to Admin and delegates.
func Start(ctx context.Context, cfg conf.Metrics) {
	admin := conf.Admin{Enabled: cfg.Enabled, Listen_: cfg.Listen_}
	StartWithController(ctx, admin, getController())
}

// StartAdmin starts the panel from a conf.Admin value.
func StartAdmin(ctx context.Context, cfg conf.Admin) {
	StartWithController(ctx, cfg, getController())
}

func StartWithController(ctx context.Context, cfg conf.Admin, c Controller) {
	if !cfg.Enabled {
		return
	}
	SetController(c)
	if c.ConfigPath != "" {
		SetConfigPath(c.ConfigPath)
	}
	tok := c.Token
	if tok == "" {
		tok = cfg.Token
	}
	SetAdminToken(tok)
	cert := c.TLSCert
	if cert == "" {
		cert = cfg.TLSCert
	}
	key := c.TLSKey
	if key == "" {
		key = cfg.TLSKey
	}
	SetTLS(cert, key)

	mux := http.NewServeMux()
	// /metrics carries the same snapshot as /api/status, so it is covered
	// by the token when one is configured. Without a token it stays public
	// for Prometheus scraping.
	mux.HandleFunc("/metrics", requireAuth(Handler))
	mux.HandleFunc("/api/status", requireAuth(statusHandler))
	mux.HandleFunc("/api/config", requireAuth(configHandler))
	mux.HandleFunc("/api/config/backups", requireAuth(configBackupsHandler))
	mux.HandleFunc("/api/config/restore", requireAuth(configRestoreHandler))
	mux.HandleFunc("/api/control", requireAuth(controlHandler))
	mux.HandleFunc("/api/logs", requireAuth(logsHandler))
	mux.HandleFunc("/api/logs/stream", requireAuth(logsStreamHandler))
	mux.HandleFunc("/api/log", requireAuth(logLevelHandler))
	mux.HandleFunc("/api/version", requireAuth(versionHandler))
	mux.HandleFunc("/api/targets", requireAuth(targetsHandler))
	mux.HandleFunc("/api/runtime", requireAuth(runtimeHandler))
	mux.HandleFunc("/api/listeners", requireAuth(listenersHandler))
	mux.HandleFunc("/api/listeners/", requireAuth(listenersActionHandler))
	mux.HandleFunc("/api/core/drain", requireAuth(coreDrainHandler))
	mux.HandleFunc("/api/net/interfaces", requireAuth(interfacesHandler))
	mux.HandleFunc("/api/net/firewall", requireAuth(firewallHandler))
	mux.HandleFunc("/api/diag/ping", requireAuth(diagPingHandler))
	mux.HandleFunc("/api/diag/dump", requireAuth(diagDumpHandler))
	mux.HandleFunc("/", rootHandler)

	listener, err := net.Listen("tcp", cfg.Listen_)
	if err != nil {
		flog.Errorf("admin listener failed on %s: %v", cfg.Listen_, err)
		return
	}

	server := &http.Server{
		Handler: mux,
		// No WriteTimeout: it covers the whole response including
		// streaming bodies, so any finite value kills the SSE endpoint
		// (/api/logs/stream) after that long. Reads stay bounded and
		// clients disconnecting are detected via r.Context().Done().
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shCtx)
	}()

	useTLS := cert != "" && key != ""
	go func() {
		var serveErr error
		if useTLS {
			serveErr = server.ServeTLS(listener, cert, key)
		} else {
			serveErr = server.Serve(listener)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			flog.Errorf("admin server error: %v", serveErr)
		}
	}()

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	flog.Infof("control panel enabled on %s://%s", scheme, cfg.Listen_)
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// Serve the UI without auth: a browser navigating to / cannot send an
	// Authorization header on first load, so gating here would make the
	// token prompt unreachable. All /api/* data endpoints stay protected.
	dashboardHandler(w, r)
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
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte("id,remote_addr,local_addr,status,uptime_seconds,bytes_in,bytes_out,active_streams\n"))
		for _, c := range snap.Connections {
			_, _ = w.Write([]byte(strings.Join([]string{
				strconv.FormatUint(c.ID, 10), csvField(c.RemoteAddr), csvField(c.LocalAddr), csvField(c.Status),
				strconv.FormatInt(c.UptimeSeconds, 10),
				strconv.FormatUint(c.BytesIn, 10), strconv.FormatUint(c.BytesOut, 10),
				strconv.FormatInt(c.ActiveStreams, 10),
			}, ",") + "\n"))
		}
		return
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

func backupConfigFile(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	ts := time.Now().UTC().Format("20060102-150405.000000000")
	bak := path + ".bak." + ts
	if err := os.WriteFile(bak, data, 0600); err != nil {
		return err
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) > 10 {
		sort.Strings(matches)
		for _, old := range matches[:len(matches)-10] {
			_ = os.Remove(old)
		}
	}
	return nil
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
			flog.Auditf("config rejected from %s: %v", remoteIP(r), err)
			return
		}
		if path == "" {
			http.Error(w, "config path unknown, cannot save", http.StatusInternalServerError)
			return
		}
		if err := backupConfigFile(path); err != nil {
			flog.Errorf("config backup failed (%s): %v", path, err)
		}
		if err := os.WriteFile(path, []byte(req.YAML), 0600); err != nil {
			http.Error(w, "cannot write config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = os.Chmod(path, 0600)
		flog.Infof("config updated via control panel (%s)", path)
		flog.Auditf("config updated via control panel (%s) from %s", path, remoteIP(r))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved", "message": "config saved - restart to apply"})
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type backupEntry struct {
	File    string `json:"file"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

func configBackupsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := getConfigPath()
	if path == "" {
		http.Error(w, "config path unknown", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		// Accept {} or {"action":"save"} for explicit backup creation.
		// Backups are otherwise created automatically on PUT /api/config.
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if a := strings.ToLower(strings.TrimSpace(req.Action)); a != "" && a != "save" {
			http.Error(w, "unknown action (want save)", http.StatusBadRequest)
			return
		}
		if err := backupConfigFile(path); err != nil {
			http.Error(w, "cannot save backup: "+err.Error(), http.StatusInternalServerError)
			return
		}
		flog.Auditf("config backup saved (%s) by %s", path, remoteIP(r))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
		return
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	entries := make([]backupEntry, 0, len(matches))
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil {
			continue
		}
		entries = append(entries, backupEntry{
			File:    filepath.Base(m),
			Path:    m,
			Size:    st.Size(),
			ModTime: st.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	if entries == nil {
		entries = []backupEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"backups": entries, "count": len(entries)})
}

func configRestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.File) == "" {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	cfgPath := getConfigPath()
	if cfgPath == "" {
		http.Error(w, "config path unknown", http.StatusInternalServerError)
		return
	}
	dir := filepath.Dir(cfgPath)
	var bakPath string
	if filepath.IsAbs(req.File) {
		bakPath = filepath.Clean(req.File)
	} else {
		bakPath = filepath.Join(dir, filepath.Base(req.File))
	}
	// Contain within config dir and require backup naming.
	if filepath.Dir(bakPath) != dir {
		http.Error(w, "invalid file (must be in config dir)", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(filepath.Base(bakPath), filepath.Base(cfgPath)+".bak.") {
		http.Error(w, "invalid file (not a backup)", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(bakPath)
	if err != nil {
		http.Error(w, "cannot read backup: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := conf.LoadFromBytes(data); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if err := backupConfigFile(cfgPath); err != nil {
		flog.Errorf("config backup before restore failed: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		http.Error(w, "cannot restore config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = os.Chmod(cfgPath, 0600)
	flog.Infof("config restored from %s via control panel", bakPath)
	flog.Auditf("config restored from %s to %s by %s", bakPath, cfgPath, remoteIP(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "restored", "file": filepath.Base(bakPath)})
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
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
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
	flog.Auditf("control %s from %s err=%v running=%v", action, remoteIP(r), err, getRunning())
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "running": getRunning()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "action": req.Action, "running": getRunning()})
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

func logLevelHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"level":     strings.ToLower(flog.LevelString()),
			"level_int": flog.GetLevel(),
		})
	case http.MethodPost, http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var req struct {
			Level string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		lv := strings.ToLower(strings.TrimSpace(req.Level))
		var lvl int
		var ok bool
		switch lv {
		case "none":
			lvl, ok = -1, true
		case "debug":
			lvl, ok = 0, true
		case "info":
			lvl, ok = 1, true
		case "warn":
			lvl, ok = 2, true
		case "error":
			lvl, ok = 3, true
		case "fatal":
			lvl, ok = 4, true
		default:
			ok = false
		}
		if !ok {
			http.Error(w, "unknown level (want none|debug|info|warn|error|fatal)", http.StatusBadRequest)
			return
		}
		flog.SetLevel(lvl)
		flog.Auditf("log level set to %s from %s", lv, remoteIP(r))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "level": lv})
	default:
		w.Header().Set("Allow", "GET, POST, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func targetsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
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
	targets := TopTargets(limit)
	if targets == nil {
		targets = []TargetStat{}
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte("target,bytes_in,bytes_out,success,failure\n"))
		for _, t := range targets {
			_, _ = w.Write([]byte(csvField(t.Target) + "," + strconv.FormatUint(t.BytesIn, 10) + "," + strconv.FormatUint(t.BytesOut, 10) + "," + strconv.FormatUint(t.Success, 10) + "," + strconv.FormatUint(t.Failure, 10) + "\n"))
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"targets":   targets,
		"count":     len(targets),
		"listeners": configListeners(),
	})
}

// csvField quotes a single CSV field per RFC 4180 and neutralizes formula
// injection: values starting with =,+,-,@ get a leading single quote so
// spreadsheet apps treat them as text. Leading spaces/tabs are checked too:
// Excel trims them before evaluating a formula (" =1+1" still executes).
func csvField(s string) string {
	if s == "" {
		return ""
	}
	trimmed := strings.TrimLeft(s, " \t")
	if trimmed != "" {
		if c := trimmed[0]; c == '=' || c == '+' || c == '-' || c == '@' {
			s = "'" + s
		}
	}
	if strings.ContainsAny(s, ",\"\r\n") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}

func configListeners() []map[string]any {
	path := getConfigPath()
	if path == "" {
		return []map[string]any{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []map[string]any{}
	}
	cfg, err := conf.LoadFromBytes(data)
	if err != nil {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, s := range cfg.SOCKS5 {
		out = append(out, map[string]any{"type": "socks5", "listen": s.Listen_})
	}
	for _, f := range cfg.Forward {
		out = append(out, map[string]any{"type": "forward", "listen": f.Listen_, "target": f.Target, "protocol": f.Protocol})
	}
	if out == nil {
		return []map[string]any{}
	}
	return out
}

func runtimeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Single ReadMemStats per scrape (STW): don't call RuntimeNow() here,
	// which would take a second back-to-back sample.
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	goroutines := runtime.NumGoroutine()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"goroutines":      goroutines,
		"mem_alloc_bytes": m.Alloc,
		"mem_sys_bytes":   m.Sys,
		"mem_heap_bytes":  m.HeapAlloc,
		"mem_gc_cycles":   m.NumGC,
		"go_version":      runtime.Version(),
		"goos":            runtime.GOOS,
		"goarch":          runtime.GOARCH,
		"num_cpu":         runtime.NumCPU(),
		"timestamp":       time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func filteredLogs(level string, tail int) []flog.LogEntry {
	entries := flog.Logs(tail * 2)
	if level != "" && level != "all" {
		filtered := entries[:0]
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
	return entries
}

func logsStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	level := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("level")))
	sinceStr := r.URL.Query().Get("since")
	if sinceStr == "" {
		sinceStr = r.Header.Get("Last-Event-ID")
	}
	var sinceID uint64
	if sinceStr != "" {
		if n, err := strconv.ParseUint(strings.TrimSpace(sinceStr), 10, 64); err == nil {
			sinceID = n
		}
	}

	// writeEntry sends one SSE event; false means the client is gone.
	writeEntry := func(e flog.LogEntry) bool {
		data, _ := json.Marshal(e)
		if _, err := w.Write([]byte("id: " + strconv.FormatUint(e.ID, 10) + "\n")); err != nil {
			return false
		}
		if _, err := w.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
			return false
		}
		return true
	}

	// lastID is the stable resume cursor (flog entry ID, also sent as the
	// SSE event id so Last-Event-ID works across reconnects). Entries are
	// ascending by ID, so everything after lastID is exactly the missed set.
	lastID := sinceID
	// Initial batch: entries newer than sinceID; fresh connects get the
	// last 200 to avoid dumping up to 1000.
	entries := filteredLogs(level, 1000)
	start := 0
	for start < len(entries) && entries[start].ID <= sinceID {
		start++
	}
	if sinceID == 0 && len(entries)-start > 200 {
		start = len(entries) - 200
	}
	for i := start; i < len(entries); i++ {
		if !writeEntry(entries[i]) {
			return
		}
		lastID = entries[i].ID
	}
	flusher.Flush()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	const pingEvery = 15 // comment ping every 15s for idle intermediaries
	ticks := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			ticks++
			cur := filteredLogs(level, 1000)
			sent := false
			for _, e := range cur {
				if e.ID <= lastID {
					continue
				}
				if !writeEntry(e) {
					return
				}
				lastID = e.ID
				sent = true
			}
			if !sent && ticks%pingEvery == 0 {
				if _, err := w.Write([]byte(": ping\n\n")); err != nil {
					return
				}
			}
			flusher.Flush()
		}
	}
}

func listenersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var listeners []map[string]any
	if c := getController(); c.Listeners != nil {
		names := c.Listeners()
		listeners = make([]map[string]any, 0, len(names))
		for _, n := range names {
			entry := map[string]any{"id": n, "name": n}
			if c.ListenerRunning != nil {
				entry["running"] = c.ListenerRunning(n)
			}
			listeners = append(listeners, entry)
		}
		if listeners == nil {
			listeners = []map[string]any{}
		}
		if len(listeners) == 0 {
			// Registry wired but nothing registered (SOCKS5/forward
			// listeners are not tracked yet): show static config entries
			// so the UI still lists what is configured.
			listeners = configListeners()
		}
	} else {
		// No registry wired: fall back to static config entries so the UI
		// still shows what is configured. These carry no running state.
		listeners = configListeners()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"listeners": listeners, "count": len(listeners)})
}

func listenersActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/listeners/")
	// Use LastIndex so IDs containing "/" (or %2F decoded to "/") don't
	// split. Action is always the final segment.
	rest = strings.Trim(rest, "/")
	slash := strings.LastIndex(rest, "/")
	if slash < 0 {
		http.Error(w, "want POST /api/listeners/{id}/{start|stop}", http.StatusBadRequest)
		return
	}
	id, action := rest[:slash], strings.ToLower(strings.TrimSpace(rest[slash+1:]))
	if id == "" || (action != "start" && action != "stop") {
		http.Error(w, "want POST /api/listeners/{id}/{start|stop}", http.StatusBadRequest)
		return
	}
	c := getController()
	var err error
	switch action {
	case "start":
		if c.ListenerStart != nil {
			err = c.ListenerStart(id)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "listener start not implemented"})
			return
		}
	case "stop":
		if c.ListenerStop != nil {
			err = c.ListenerStop(id)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "listener stop not implemented"})
			return
		}
	default:
		http.Error(w, "unknown action (want start|stop)", http.StatusBadRequest)
		return
	}
	flog.Auditf("listener %s %s from %s err=%v", id, action, remoteIP(r), err)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": id, "action": action})
}

func coreDrainHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		TimeoutSec *int `json:"timeout_sec"`
		Timeout    *int `json:"timeout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	timeoutSec := 10
	if req.TimeoutSec != nil {
		timeoutSec = *req.TimeoutSec
	} else if req.Timeout != nil {
		timeoutSec = *req.Timeout
	} else if s := strings.TrimSpace(r.URL.Query().Get("timeout")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			timeoutSec = n
		}
	}
	// Bound the wait: no WriteTimeout is set (SSE streams), so cap here to
	// keep drain from blocking the admin handler indefinitely.
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	if timeoutSec > 12 {
		timeoutSec = 12
	}
	c := getController()
	var err error
	if c.Drain != nil {
		// Run blocking drain in background so client disconnect aborts
		// the wait instead of holding the handler up to 12s.
		type drainRes struct{ err error }
		done := make(chan drainRes, 1)
		go func() { done <- drainRes{c.Drain(timeoutSec)} }()
		select {
		case <-r.Context().Done():
			flog.Auditf("core drain cancelled (client gone) from %s timeout=%d", remoteIP(r), timeoutSec)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "client disconnected"})
			return
		case res := <-done:
			err = res.err
		}
	} else {
		// No drain hook wired: report not-implemented instead of a
		// misleading success.
		flog.Auditf("core drain requested from %s timeout=%d (not wired)", remoteIP(r), timeoutSec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "drain not implemented"})
		return
	}
	flog.Auditf("core drain from %s timeout=%d err=%v", remoteIP(r), timeoutSec, err)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "detail": "drain requested"})
}
