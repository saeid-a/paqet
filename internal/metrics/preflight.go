package metrics

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"paqet/internal/conf"
	"paqet/internal/diag"
)

// Check is a single preflight safety check.
type Check struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // pass|warn|fail|unknown
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// preflightResponse aggregates launch-readiness checks for the Safety page.
// The KCP key value is never returned, only KeySet.
type preflightResponse struct {
	Role      string              `json:"role"`
	Port      int                 `json:"port"`
	ServerIP  string              `json:"server_ip"`
	Checks    []Check             `json:"checks"`
	ServerFix []string            `json:"server_fix"`
	ClientFix []string            `json:"client_fix"`
	Firewall  diag.FirewallResult `json:"firewall"`
	KeySet    bool                `json:"key_set"`
}

func mkCheck(id, label, status, detail, fix string) Check {
	return Check{ID: id, Label: label, Status: status, Detail: detail, Fix: fix}
}

// preflightHandler serves GET /api/safety/preflight.
func preflightHandler(w http.ResponseWriter, r *http.Request) {
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

	data, err := os.ReadFile(getConfigPath())
	var cfg *conf.Conf
	if err == nil {
		cfg, err = conf.LoadFromBytes(data)
	}
	if err != nil {
		detail := err.Error()
		if getConfigPath() == "" {
			detail = "config path unknown"
		}
		_ = json.NewEncoder(w).Encode(preflightResponse{
			Role:      "unknown",
			Checks:    []Check{mkCheck("config", "Config", "fail", detail, "fix the config file, then retry preflight")},
			ServerFix: []string{},
			ClientFix: []string{},
			Firewall:  diag.FirewallResult{Missing: []string{}, Fix: []string{}},
		})
		return
	}

	role := cfg.Role
	serverHost := ""
	if host, _, splitErr := net.SplitHostPort(cfg.Server.Addr_); splitErr == nil {
		serverHost = host
	} else {
		serverHost = strings.TrimSpace(cfg.Server.Addr_)
	}
	if cfg.Server.Addr != nil {
		if serverHost == "" && cfg.Server.Addr.IP != nil {
			serverHost = cfg.Server.Addr.IP.String()
		}
	}
	port := cfg.Network.Port
	if role == "client" && cfg.Server.Addr != nil && cfg.Server.Addr.Port != 0 {
		port = cfg.Server.Addr.Port
	}
	keySet := cfg.Transport.KCP != nil && cfg.Transport.KCP.Key != ""

	checks := make([]Check, 0, 8)

	// role
	switch role {
	case "client", "server":
		checks = append(checks, mkCheck("role", "Role", "pass", "role="+role, ""))
	default:
		checks = append(checks, mkCheck("role", "Role", "fail", "unknown role "+strconv.Quote(role), "set role to client or server"))
	}

	// admin-bind
	bind := cfg.EffectiveAdmin().Listen_
	if bindIsLoopback(bind) {
		checks = append(checks, mkCheck("admin-bind", "Admin bind", "pass", "admin bind "+bind+" is loopback-only", ""))
	} else if cfg.Admin.Token != "" || getAdminToken() != "" {
		checks = append(checks, mkCheck("admin-bind", "Admin bind", "pass", "admin bind "+bind+" is protected by token", ""))
	} else {
		checks = append(checks, mkCheck("admin-bind", "Admin bind", "fail", "admin bind "+bind+" is exposed without a token", "set admin.token (>=16 chars) or bind 127.0.0.1"))
	}

	// network-interface (+ interfaces lookup bounded by timeout)
	if v, ok := runDiagWithTimeout(r.Context(), 5*time.Second, func() any {
		_, err := diag.Interfaces()
		return err
	}); !ok {
		checks = append(checks, mkCheck("network-interface", "Network interface", "unknown", "interface lookup timed out", "retry preflight"))
	} else if err, _ := v.(error); err != nil {
		checks = append(checks, mkCheck("network-interface", "Network interface", "unknown", "interface lookup failed: "+err.Error(), "retry preflight"))
	} else if _, err := net.InterfaceByName(cfg.Network.Interface_); err != nil {
		checks = append(checks, mkCheck("network-interface", "Network interface", "fail", "interface "+cfg.Network.Interface_+" not found: "+err.Error(), "set network.interface to an existing interface"))
	} else {
		checks = append(checks, mkCheck("network-interface", "Network interface", "pass", "interface "+cfg.Network.Interface_+" exists", ""))
	}

	// ipv4
	switch {
	case cfg.Network.IPv4.Addr != nil:
		checks = append(checks, mkCheck("ipv4", "IPv4", "pass", "ipv4 "+cfg.Network.IPv4.Addr_+" parses", ""))
	case strings.TrimSpace(cfg.Network.IPv4.Addr_) != "":
		checks = append(checks, mkCheck("ipv4", "IPv4", "fail", "ipv4 "+strconv.Quote(cfg.Network.IPv4.Addr_)+" is invalid", "set network.ipv4.addr to a valid host:port"))
	default:
		checks = append(checks, mkCheck("ipv4", "IPv4", "warn", "ipv4 not configured", "set network.ipv4.addr"))
	}

	// router-mac
	if _, err := net.ParseMAC(cfg.Network.IPv4.RouterMac_); err != nil {
		checks = append(checks, mkCheck("router-mac", "Router MAC", "fail", "invalid router MAC: "+err.Error(), "set network.ipv4.router_mac to the gateway MAC"))
	} else {
		checks = append(checks, mkCheck("router-mac", "Router MAC", "pass", "router MAC parses", ""))
	}

	// endpoints
	if role == "server" {
		if strings.TrimSpace(cfg.Listen.Addr_) != "" {
			checks = append(checks, mkCheck("endpoints", "Endpoints", "pass", "listen "+cfg.Listen.Addr_, ""))
		} else {
			checks = append(checks, mkCheck("endpoints", "Endpoints", "fail", "server listen address is not configured", "set listen.addr"))
		}
	} else {
		if len(cfg.SOCKS5)+len(cfg.Forward) > 0 {
			checks = append(checks, mkCheck("endpoints", "Endpoints", "pass", strconv.Itoa(len(cfg.SOCKS5)+len(cfg.Forward))+" local endpoint(s) configured", ""))
		} else {
			checks = append(checks, mkCheck("endpoints", "Endpoints", "fail", "client has no socks5 or forward endpoints", "add a socks5 or forward entry"))
		}
	}

	// transport-key (never echo the key value)
	if keySet {
		checks = append(checks, mkCheck("transport-key", "Transport key", "pass", "kcp key is set", ""))
	} else {
		checks = append(checks, mkCheck("transport-key", "Transport key", "fail", "kcp key is missing", "set transport.kcp.key"))
	}

	// firewall
	var fw diag.FirewallResult
	fw.Port = port
	if runtime.GOOS != "linux" {
		checks = append(checks, mkCheck("firewall", "Firewall", "unknown", "firewall check unsupported on "+runtime.GOOS, "apply the server_fix/client_fix rules manually on Linux"))
	} else if v, ok := runDiagWithTimeout(r.Context(), 5*time.Second, func() any {
		return diag.FirewallCheck(port)
	}); !ok {
		checks = append(checks, mkCheck("firewall", "Firewall", "unknown", "firewall check timed out", "retry preflight"))
	} else {
		fw, _ = v.(diag.FirewallResult)
		switch {
		case fw.Error != "":
			checks = append(checks, mkCheck("firewall", "Firewall", "fail", fw.Error, strings.Join(fw.Fix, "\n")))
		case fw.Present:
			checks = append(checks, mkCheck("firewall", "Firewall", "pass", "expected iptables rules present", ""))
		default:
			checks = append(checks, mkCheck("firewall", "Firewall", "warn", strconv.Itoa(len(fw.Missing))+" expected iptables rule(s) missing", strings.Join(fw.Fix, "\n")))
		}
	}
	if fw.Fix == nil {
		fw.Fix = []string{}
	}
	if fw.Missing == nil {
		fw.Missing = []string{}
	}
	serverFix := append([]string{}, fw.Fix...)

	var clientFix []string
	if role == "client" {
		clientFix = diag.ClientFirewallFix(serverHost, port)
	}
	if clientFix == nil {
		clientFix = []string{}
	}

	_ = json.NewEncoder(w).Encode(preflightResponse{
		Role:      role,
		Port:      port,
		ServerIP:  serverHost,
		Checks:    checks,
		ServerFix: serverFix,
		ClientFix: clientFix,
		Firewall:  fw,
		KeySet:    keySet,
	})
}
