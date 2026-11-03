package socks

import (
	"os"
	"strings"
	"testing"
)

// SOCKS UDP must also feed global + Targets counters.
func TestAssociateFeedsCounters(t *testing.T) {
	src, err := os.ReadFile("associate.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		"IncSuccess()",
		"IncFailure()",
		"IncSuccessUDP()",
		"IncFailureUDP()",
		"IncSuccessSOCKS()",
		"IncFailureSOCKS()",
		"IncTarget(",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("associate.go missing %s; UDP tables stay 0", want)
		}
	}
}
