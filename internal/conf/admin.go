package conf

import "fmt"

type Admin struct {
	Enabled bool   `yaml:"enabled"`
	Listen_ string `yaml:"listen"`
	Token   string `yaml:"token"`
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
}

func (a *Admin) setDefaults() {
	if a.Listen_ == "" {
		a.Listen_ = "127.0.0.1:9090"
	}
}

func (a *Admin) validate() []error {
	if !a.Enabled {
		return nil
	}
	var errors []error
	addr, err := validateTCPAddr(a.Listen_, true)
	if err != nil {
		errors = append(errors, err)
	} else if addr.IP == nil || !addr.IP.IsLoopback() {
		if a.Token == "" {
			errors = append(errors, fmt.Errorf("admin token is required when listening on a non-loopback address"))
		}
	}
	if a.Token != "" && len(a.Token) < 16 {
		errors = append(errors, fmt.Errorf("admin token must be at least 16 characters"))
	}
	if (a.TLSCert == "") != (a.TLSKey == "") {
		errors = append(errors, fmt.Errorf("admin tls_cert and tls_key must both be set"))
	}
	return errors
}
