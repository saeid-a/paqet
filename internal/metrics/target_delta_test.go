package metrics

import (
	"fmt"
	"testing"
	"time"
)

// Behavioral replacement for source-grep counter tests: verifies IncTarget
// actually records byte/success deltas visible via TopTargets.
func TestIncTargetRecordsDelta(t *testing.T) {
	target := fmt.Sprintf("test-behavioral-%d.invalid:1234", time.Now().UnixNano())
	before := TopTargets(500)
	var beforeIn, beforeOut, beforeSucc uint64
	for _, s := range before {
		if s.Target == target {
			beforeIn, beforeOut, beforeSucc = s.BytesIn, s.BytesOut, s.Success
		}
	}
	IncTarget(target, 10, 20, true)
	IncTarget(target, 5, 0, true)
	found := false
	for _, s := range TopTargets(500) {
		if s.Target == target {
			found = true
			if s.BytesIn != beforeIn+15 {
				t.Fatalf("want in %d, got %d", beforeIn+15, s.BytesIn)
			}
			if s.BytesOut != beforeOut+20 {
				t.Fatalf("want out %d, got %d", beforeOut+20, s.BytesOut)
			}
			if s.Success != beforeSucc+2 {
				t.Fatalf("want succ %d, got %d", beforeSucc+2, s.Success)
			}
		}
	}
	if !found {
		t.Fatal("target not found after IncTarget")
	}
}

func TestIncTargetFailureDelta(t *testing.T) {
	target := fmt.Sprintf("test-fail-%d.invalid:1234", time.Now().UnixNano())
	IncTarget(target, 0, 0, false)
	for _, s := range TopTargets(500) {
		if s.Target == target {
			if s.Failure < 1 {
				t.Fatalf("want failure >=1, got %d", s.Failure)
			}
			return
		}
	}
	t.Fatal("target not found")
}
