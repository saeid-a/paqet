// Package diag provides lightweight network diagnostics for the admin API.
// It intentionally avoids privileged packet I/O: checks that need raw
// sockets (ping/dump) are stubs until wired to a proper implementation.
package diag

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// InterfaceInfo describes a single OS network interface.
type InterfaceInfo struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
	IsUp  bool     `json:"is_up"`
	MTU   int      `json:"mtu"`
}

// Interfaces lists OS network interfaces with addresses, flags and MTU.
func Interfaces() ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]InterfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		info := InterfaceInfo{
			Name: iface.Name,
			IsUp: iface.Flags&net.FlagUp != 0,
			MTU:  iface.MTU,
		}
		if addrs, err := iface.Addrs(); err == nil {
			for _, a := range addrs {
				info.Addrs = append(info.Addrs, a.String())
			}
		}
		if info.Addrs == nil {
			info.Addrs = []string{}
		}
		out = append(out, info)
	}
	return out, nil
}

// FirewallResult reports whether the expected iptables rules for port are present.
type FirewallResult struct {
	Supported bool     `json:"supported"`
	Present   bool     `json:"present"`
	Missing   []string `json:"missing"`
	Fix       []string `json:"fix"`
	Port      int      `json:"port"`
	Error     string   `json:"error,omitempty"`
}

// FirewallCheck inspects iptables rules for the given port.
// On Linux it runs `iptables -S` (plus the raw and mangle tables) via exec
// and looks for the NOTRACK and RST-DROP rules. On other OSes it returns
// supported:false.
func FirewallCheck(port int) FirewallResult {
	res := FirewallResult{
		Supported: true,
		Port:      port,
		Missing:   []string{},
		Fix:       []string{},
	}
	if runtime.GOOS != "linux" {
		res.Supported = false
		return res
	}
	if port <= 0 || port > 65535 {
		res.Present = false
		res.Error = fmt.Sprintf("invalid port %d", port)
		return res
	}

	wantNotrackIn := fmt.Sprintf("raw PREROUTING NOTRACK --dport %d", port)
	wantNotrackOut := fmt.Sprintf("raw OUTPUT NOTRACK --sport %d", port)
	wantRSTDrop := fmt.Sprintf("mangle OUTPUT DROP RST --sport %d", port)
	fixNotrackIn := fmt.Sprintf("sudo iptables -t raw -A PREROUTING -p tcp --dport %d -j NOTRACK", port)
	fixNotrackOut := fmt.Sprintf("sudo iptables -t raw -A OUTPUT -p tcp --sport %d -j NOTRACK", port)
	fixRSTDrop := fmt.Sprintf("sudo iptables -t mangle -A OUTPUT -p tcp --sport %d --tcp-flags RST RST -j DROP", port)

	combined, err := dumpIPTables()
	if err != nil {
		res.Present = false
		res.Missing = []string{wantNotrackIn, wantNotrackOut, wantRSTDrop}
		res.Fix = []string{fixNotrackIn, fixNotrackOut, fixRSTDrop}
		res.Error = err.Error()
		return res
	}

	var hasNotrackDport, hasNotrackSport, hasRSTDrop = firewallRulesPresent(combined, port)

	if !hasNotrackDport {
		res.Missing = append(res.Missing, wantNotrackIn)
		res.Fix = append(res.Fix, fixNotrackIn)
	}
	if !hasNotrackSport {
		res.Missing = append(res.Missing, wantNotrackOut)
		res.Fix = append(res.Fix, fixNotrackOut)
	}
	if !hasRSTDrop {
		res.Missing = append(res.Missing, wantRSTDrop)
		res.Fix = append(res.Fix, fixRSTDrop)
	}
	res.Present = len(res.Missing) == 0
	return res
}

// firewallRulesPresent reports which expected rules appear in combined
// iptables -S output. Matches on "--dport <port>" / "--sport <port>" with a
// trailing-digit guard so port 80 does not match a rule for 8080.
func firewallRulesPresent(combined string, port int) (hasNotrackDport, hasNotrackSport, hasRSTDrop bool) {
	dport := fmt.Sprintf("--dport %d", port)
	sport := fmt.Sprintf("--sport %d", port)
	for _, line := range strings.Split(combined, "\n") {
		if strings.Contains(line, "NOTRACK") {
			if hasPortToken(line, dport) {
				hasNotrackDport = true
			}
			if hasPortToken(line, sport) {
				hasNotrackSport = true
			}
		}
		if strings.Contains(line, "DROP") && strings.Contains(line, "RST") && hasPortToken(line, sport) {
			hasRSTDrop = true
		}
	}
	return hasNotrackDport, hasNotrackSport, hasRSTDrop
}

// hasPortToken reports whether line contains needle where the char after the
// match is not a digit (or end of line).
func hasPortToken(line, needle string) bool {
	idx := 0
	for {
		i := strings.Index(line[idx:], needle)
		if i < 0 {
			return false
		}
		pos := idx + i + len(needle)
		if pos >= len(line) || line[pos] < '0' || line[pos] > '9' {
			return true
		}
		idx = pos
	}
}

// dumpIPTables concatenates `iptables -S` output across filter/raw/mangle tables.
func dumpIPTables() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var sb strings.Builder
	for _, table := range []string{"", "raw", "mangle"} {
		var cmd *exec.Cmd
		if table == "" {
			cmd = exec.CommandContext(ctx, "iptables", "-S")
		} else {
			cmd = exec.CommandContext(ctx, "iptables", "-t", table, "-S")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("iptables -S failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
		sb.Write(out)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

// PingResult is the result of a ping diagnostic run.
type PingResult struct {
	Error string `json:"error"`
}

// PingSummary runs a ping diagnostic against cfgPath.
// Raw-socket ping is not wired into the admin API yet, so this is a stub.
func PingSummary(cfgPath string, timeoutSec int) PingResult {
	return PingResult{Error: "not wired"}
}

// DumpResult is the result of a packet capture run.
type DumpResult struct {
	Error string `json:"error"`
}

// DumpCapture captures count packets on port.
// Raw-socket capture is not wired into the admin API yet, so this is a stub.
func DumpCapture(port, count int) DumpResult {
	return DumpResult{Error: "not wired"}
}
