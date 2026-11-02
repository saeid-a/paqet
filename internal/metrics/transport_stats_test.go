package metrics

import "testing"

func TestTransportStatsInSnapshot(t *testing.T) {
	SetTransportSnapshot(TransportStats{SndQueue: 3, RcvQueue: 1, SndBuf: 5, RetransSegs: 10, LostSegs: 2, FECRecovered: 4})
	snap := SnapshotNow()
	if snap.Transport.SndQueue != 3 {
		t.Fatalf("want sndQueue 3, got %d", snap.Transport.SndQueue)
	}
	if snap.KCP.Retransmits == 0 {
		t.Fatalf("legacy retransmits should still populate")
	}
}
