package kcp

import "testing"

func TestSampleNowNoSessions(t *testing.T) {
	s := SampleNow()
	if s.SRTT < 0 {
		t.Fatalf("negative srtt")
	}
	// with no sessions, queues come from DefaultSnmp.Copy, never negative
}
