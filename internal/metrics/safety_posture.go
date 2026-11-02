package metrics

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"runtime"

	"paqet/internal/conf"
)

// PostureResponse aggregates the panel's security posture for the Overview
// card. The token value is never returned, only TokenSet.
type PostureResponse struct {
	Bind              string `json:"bind"`
	IsLoopback        bool   `json:"is_loopback"`
	TokenSet          bool   `json:"token_set"`
	TLSEnabled        bool   `json:"tls_enabled"`
	Exposure          string `json:"exposure"`
	Role              string `json:"role"`
	ConfigPath        string `json:"config_path"`
	FirewallSupported bool   `json:"firewall_supported"`
}

// defaultAdminBind is used when no config file is available.
const defaultAdminBind = "127.0.0.1:9090"

// bindIsLoopback reports whether the bind host is a loopback IP.
// A missing/empty host (e.g. ":9090") parses to nil and is non-loopback.
func bindIsLoopback(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		host = bind
	}
	return net.ParseIP(host).IsLoopback()
}

// exposureFor derives safe/warn/critical from bind + client loopback state,
// token length, and TLS. Short tokens (1-15 chars) are always critical;
// a valid token (16+) is always safe. Without a token, anything reachable
// beyond loopback is critical; loopback-only without TLS is warn.
func exposureFor(bindLoopback, clientLoopback bool, token string, tls bool) string {
	if len(token) > 0 && len(token) < 16 {
		return "critical"
	}
	if len(token) >= 16 {
		return "safe"
	}
	if tls && bindLoopback && clientLoopback {
		return "safe"
	}
	if !bindLoopback || !clientLoopback {
		return "critical"
	}
	return "warn"
}

func buildPosture(r *http.Request) PostureResponse {
	bind := defaultAdminBind
	role := "unknown"
	if path := getConfigPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if cfg, err := conf.LoadFromBytes(data); err == nil {
				if a := cfg.EffectiveAdmin().Listen_; a != "" {
					bind = a
				}
				if cfg.Role != "" {
					role = cfg.Role
				}
			}
		}
	}
	token := getAdminToken()
	tls := getTLSCert() != "" && getTLSKey() != ""
	bindLoopback := bindIsLoopback(bind)
	clientLoopback := net.ParseIP(remoteIP(r)).IsLoopback()
	return PostureResponse{
		Bind:              bind,
		IsLoopback:        bindLoopback,
		TokenSet:          token != "",
		TLSEnabled:        tls,
		Exposure:          exposureFor(bindLoopback, clientLoopback, token, tls),
		Role:              role,
		ConfigPath:        getConfigPath(),
		FirewallSupported: runtime.GOOS == "linux",
	}
}

func postureHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(buildPosture(r))
}
