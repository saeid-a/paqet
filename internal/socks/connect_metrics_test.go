package socks

import (
	"os"
	"strings"
	"testing"
)

// SOCKS CONNECT must feed the global success/failure tables, not just Targets.
func TestConnectFeedsGlobalCounters(t *testing.T) {
	src, err := os.ReadFile("connect.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		"IncSuccess()",
		"IncFailure()",
		"IncSuccessSOCKS()",
		"IncFailureSOCKS()",
		"IncSuccessTCP()",
		"IncFailureTCP()",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("connect.go missing %s; Success/failure tables stay 0", want)
		}
	}
}
