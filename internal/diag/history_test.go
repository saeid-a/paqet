package diag

import (
	"sync"
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

func TestHistoryConcurrentRecordOrder(t *testing.T) {
	h := NewHistory()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				h.Record("dial", "", "s", nil)
			}
		}()
	}
	wg.Wait()
	list := h.List()
	if len(list) != 20 {
		t.Fatalf("want 20, got %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ID <= list[i].ID {
			t.Fatalf("newest-first violated at %d: %d <= %d", i, list[i-1].ID, list[i].ID)
		}
	}
}
