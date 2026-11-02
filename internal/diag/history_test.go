package diag

import (
	"testing"
)

// RED: ring keeps last 20, newest first.
func TestHistoryKeepsLast20(t *testing.T) {
	h := NewHistory()
	for i := 0; i < 25; i++ {
		h.Record("dial", "", "s", nil)
	}
	if got := len(h.List()); got != 20 {
		t.Fatalf("want 20, got %d", got)
	}
	if h.List()[0].ID <= h.List()[19].ID {
		t.Fatal("newest first expected")
	}
}
