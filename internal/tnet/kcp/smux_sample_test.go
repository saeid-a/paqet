package kcp

import (
	"net"
	"testing"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"

	"paqet/internal/metrics"
)

func resetStatsForTest() {
	statMu.Lock()
	sessions = make(map[*smux.Session]*kcp.UDPSession)
	streamStarted = make(map[*smux.Stream]time.Time)
	statMu.Unlock()
}

// helper: live client session with a draining server peer so OpenStream
// never blocks on the pipe.
func testPipedSession(t *testing.T) (*smux.Session, func()) {
	t.Helper()
	c1, s1 := net.Pipe()
	srv, err := smux.Server(s1, smux.DefaultConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	go func() {
		for {
			st, err := srv.AcceptStream()
			if err != nil {
				return
			}
			st.Close()
		}
	}()
	cli, err := smux.Client(c1, smux.DefaultConfig())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return cli, func() {
		untrackSession(cli)
		cli.Close()
		srv.Close()
	}
}

// Worst case: dead sessions linger in the registry and NumConns lies forever.
func TestSamplePrunesClosedSessions(t *testing.T) {
	resetStatsForTest()
	cli, cleanup := testPipedSession(t)
	defer cleanup()
	trackSession(nil, cli)
	if got := SampleNow().NumSessions; got != 1 {
		t.Fatalf("want 1 live session, got %d", got)
	}
	cli.Close()
	if got := SampleNow().NumSessions; got != 0 {
		t.Fatalf("want 0 sessions after close, got %d", got)
	}
	statMu.RLock()
	n := len(sessions)
	statMu.RUnlock()
	if n != 0 {
		t.Fatalf("want registry empty after prune, got %d", n)
	}
}

// Worst case: one hog session starves the rest; sum alone hides it.
func TestSampleMaxStreamsPerConn(t *testing.T) {
	resetStatsForTest()
	cli, cleanup := testPipedSession(t)
	defer cleanup()
	trackSession(nil, cli)
	var streams []*smux.Stream
	for i := 0; i < 3; i++ {
		st, err := cli.OpenStream()
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		streams = append(streams, st)
		noteStreamOpen(st)
	}
	s := SampleNow()
	if s.MaxStreams < 3 {
		t.Fatalf("want max >= 3, got %d", s.MaxStreams)
	}
	if s.NumStreams < 3 {
		t.Fatalf("want num >= 3, got %d", s.NumStreams)
	}
	for _, st := range streams {
		noteStreamClose(st)
		st.Close()
	}
}

// Worst case: leak vs churn look identical in counts; age tells them apart.
func TestSampleOldestStreamAge(t *testing.T) {
	resetStatsForTest()
	cli, cleanup := testPipedSession(t)
	defer cleanup()
	trackSession(nil, cli)
	st, err := cli.OpenStream()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	noteStreamOpen(st)
	statMu.Lock()
	streamStarted[st] = time.Now().Add(-120 * time.Second)
	statMu.Unlock()
	s := SampleNow()
	if s.OldestAgeSec < 90 {
		t.Fatalf("want oldest >= 90s, got %d", s.OldestAgeSec)
	}
	noteStreamClose(st)
	st.Close()
}

// Open/accept failures must split out from generic IncFailure so a dead
// session reads differently from a downstream dial failure.
func TestStrmFailureSplit(t *testing.T) {
	c1, _ := net.Pipe()
	cli, err := smux.Client(c1, smux.DefaultConfig())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	cli.Close()
	c := &Conn{Session: cli}
	baseO := metrics.SnapshotNow().Transport.SmuxOpenFail
	baseA := metrics.SnapshotNow().Transport.SmuxAcceptFail
	if _, err := c.OpenStrm(); err == nil {
		t.Fatalf("want OpenStrm error on closed session")
	}
	if _, err := c.AcceptStrm(); err == nil {
		t.Fatalf("want AcceptStrm error on closed session")
	}
	gotO := metrics.SnapshotNow().Transport.SmuxOpenFail
	gotA := metrics.SnapshotNow().Transport.SmuxAcceptFail
	if gotO != baseO+1 {
		t.Fatalf("want openFail %d, got %d", baseO+1, gotO)
	}
	if gotA != baseA+1 {
		t.Fatalf("want acceptFail %d, got %d", baseA+1, gotA)
	}
}
