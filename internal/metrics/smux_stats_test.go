package metrics

import "testing"

// RED 1: stream open accounting (opens total + high-water mark).
// Worst case this catches: slow stream leak (opens climbs, HWM climbs,
// active never drains) vs churn storm (opens climbs fast, HWM flat).
func TestStreamOpensAndHWM(t *testing.T) {
	base := SnapshotNow().Transport.StreamOpens
	IncStreams()
	IncStreams()
	DecStreams()
	snap := SnapshotNow()
	if snap.Transport.StreamOpens < base+2 {
		t.Fatalf("want opens >= base+2, got %d (base %d)", snap.Transport.StreamOpens, base)
	}
	if snap.Transport.StreamHWM < 2 {
		t.Fatalf("want HWM >= 2, got %d", snap.Transport.StreamHWM)
	}
	DecStreams() // balance the second Inc so global active is unchanged
}

// RED 2: sampler-pushed smux detail round-trips through Snapshot.
func TestTransportSmuxDetailRoundTrip(t *testing.T) {
	SetTransportSnapshot(TransportStats{
		MaxStreamsPerConn: 7,
		OldestStreamAgeSec: 90,
		SmuxOpenFail:       2,
		SmuxAcceptFail:     1,
	})
	snap := SnapshotNow()
	if snap.Transport.MaxStreamsPerConn != 7 {
		t.Fatalf("want max 7, got %d", snap.Transport.MaxStreamsPerConn)
	}
	if snap.Transport.OldestStreamAgeSec != 90 {
		t.Fatalf("want oldest 90, got %d", snap.Transport.OldestStreamAgeSec)
	}
	if snap.Transport.SmuxOpenFail != 2 || snap.Transport.SmuxAcceptFail != 1 {
		t.Fatalf("want fails 2/1, got %d/%d", snap.Transport.SmuxOpenFail, snap.Transport.SmuxAcceptFail)
	}
}

// RED 3: split smux failure counters are exposed.
func TestSmuxFailureCounters(t *testing.T) {
	baseO := SnapshotNow().Transport.SmuxOpenFail
	baseA := SnapshotNow().Transport.SmuxAcceptFail
	IncSmuxOpenFail()
	IncSmuxAcceptFail()
	snap := SnapshotNow()
	if snap.Transport.SmuxOpenFail != baseO+1 {
		t.Fatalf("want openFail %d, got %d", baseO+1, snap.Transport.SmuxOpenFail)
	}
	if snap.Transport.SmuxAcceptFail != baseA+1 {
		t.Fatalf("want acceptFail %d, got %d", baseA+1, snap.Transport.SmuxAcceptFail)
	}
}
