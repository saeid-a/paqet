package conf

import (
	"strings"
	"testing"
)

func TestValidateSocksEgress(t *testing.T) {
	valid := []string{"", "@active", "forward:127.0.0.1:18081", "127.0.0.1:5005", "[::1]:1080"}
	for _, spec := range valid {
		if err := ValidateSocksEgress(spec); err != nil {
			t.Errorf("spec %q: want nil, got %v", spec, err)
		}
	}
	invalid := []string{"@bogus", "forward:", "garbage", "h:99999", "127.0.0.1:0"}
	for _, spec := range invalid {
		if err := ValidateSocksEgress(spec); err == nil {
			t.Errorf("spec %q: want error, got nil", spec)
		}
	}
}

func TestSocksEgressValidation(t *testing.T) {
	cfg, err := LoadFromBytes([]byte("role: client\nserver:\n  addr: 127.0.0.1:9999\nnetwork:\n  interface: en0\n  ipv4:\n    addr: 127.0.0.1:0\n    router_mac: aa:bb:cc:dd:ee:ff\nsocks5:\n  - listen: 127.0.0.1:1080\n    egress: \"@active\"\ntransport:\n  protocol: kcp\n  kcp:\n    block: none\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SOCKS5[0].Egress != "@active" {
		t.Fatalf("egress = %q", cfg.SOCKS5[0].Egress)
	}

	_, err = LoadFromBytes([]byte("role: client\nserver:\n  addr: 127.0.0.1:9999\nnetwork:\n  interface: en0\n  ipv4:\n    addr: 127.0.0.1:0\n    router_mac: aa:bb:cc:dd:ee:ff\nsocks5:\n  - listen: 127.0.0.1:1080\n    egress: \"@bogus\"\ntransport:\n  protocol: kcp\n  kcp:\n    block: none\n"))
	if err == nil || !strings.Contains(err.Error(), "socks5[0]") {
		t.Fatalf("want socks5[0] validation error, got %v", err)
	}
}
