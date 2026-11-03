package conf

import (
	"net"
)

type SOCKS5 struct {
	Listen_  string `yaml:"listen"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// Egress pins this listener's exit: "" = direct (respect the app's
	// requested destination), "@active" = follow the active forward
	// (the sole running forward's target), "forward:<listen>" = pinned
	// to that forward's target, or "host:port" = custom fixed target.
	Egress string       `yaml:"egress"`
	Listen *net.UDPAddr `yaml:"-"`
}

func (c *SOCKS5) setDefaults() {}

// ValidateSocksEgress reports whether a SOCKS egress spec is well-formed.
// "" (direct), "@active" (follow active forward), "forward:<listen>"
// (pinned forward), or "host:port" (custom fixed target).
func ValidateSocksEgress(spec string) error {
	switch {
	case spec == "" || spec == "@active":
		return nil
	case len(spec) > len("forward:") && spec[:len("forward:")] == "forward:":
		if rest := spec[len("forward:"):]; rest != "" {
			return nil
		}
		return validateHostPort(spec, true)
	default:
		return validateHostPort(spec, true)
	}
}
func (c *SOCKS5) validate() []error {
	var errors []error

	addr, err := validateAddr(c.Listen_, true)
	if err != nil {
		errors = append(errors, err)
	}
	c.Listen = addr
	if err := ValidateSocksEgress(c.Egress); err != nil {
		errors = append(errors, err)
	}
	return errors
}
