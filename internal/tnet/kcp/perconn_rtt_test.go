package kcp

import (
	"testing"

	"paqet/internal/metrics"
)

// Per-connection RTT: each smux session is tied to one metrics connection.
// SampleNow must push that session's SRTT into metrics so the conn table
// RTT column shows a live value instead of "—".
func TestSamplePushesPerConnRTT(t *testing.T) {
	resetStatsForTest()
	cli, cleanup := testPipedSession(t)
	defer cleanup()

	id := metrics.RegisterConnection("test-remote", "test-local")
	trackSession(nil, cli, id)

	// Mapping must exist so SampleNow can route SRTT -> metrics connection.
	statMu.RLock()
	got, ok := sessionMetrics[cli]
	statMu.RUnlock()
	if !ok || got != id {
		t.Fatalf("want sessionMetrics[%p]==%d, got %d ok=%v", cli, id, got, ok)
	}

	SampleNow()

	// untrack must clear both maps so closed conns stop feeding RTT.
	untrackSession(cli)
	statMu.RLock()
	_, stillTracked := sessions[cli]
	_, stillMapped := sessionMetrics[cli]
	statMu.RUnlock()
	if stillTracked || stillMapped {
		t.Fatalf("want session fully untracked, tracked=%v mapped=%v", stillTracked, stillMapped)
	}
}

func TestSamplePrunesStaleMetricsMapping(t *testing.T) {
	resetStatsForTest()
	cli, cleanup := testPipedSession(t)
	defer cleanup()

	id := metrics.RegisterConnection("prune-remote", "prune-local")
	trackSession(nil, cli, id)
	cli.Close()
	SampleNow()
	statMu.RLock()
	_, stillTracked := sessions[cli]
	_, stillMapped := sessionMetrics[cli]
	statMu.RUnlock()
	if stillTracked || stillMapped {
		t.Fatalf("want pruned session fully gone, tracked=%v mapped=%v", stillTracked, stillMapped)
	}
}
