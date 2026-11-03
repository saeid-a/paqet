package client

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"paqet/internal/tnet"
)

type fakeStrm struct {
	read func([]byte) (int, error)
}

func (f *fakeStrm) Read(p []byte) (int, error)         { return f.read(p) }
func (f *fakeStrm) Write(p []byte) (int, error)        { return len(p), nil }
func (f *fakeStrm) Close() error                       { return nil }
func (f *fakeStrm) LocalAddr() net.Addr                { return nil }
func (f *fakeStrm) RemoteAddr() net.Addr               { return nil }
func (f *fakeStrm) SetDeadline(t time.Time) error      { return nil }
func (f *fakeStrm) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeStrm) SetWriteDeadline(t time.Time) error { return nil }
func (f *fakeStrm) SID() int                           { return 1 }

var _ tnet.Strm = (*fakeStrm)(nil)

func TestProbeStreamOpenFails(t *testing.T) {
	want := errors.New("tunnel down")
	_, _, err := probeStream(context.Background(), func(context.Context) (tnet.Strm, error) {
		return nil, want
	}, time.Second)
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestProbeStreamBannerSpeaksFirst(t *testing.T) {
	open := func(context.Context) (tnet.Strm, error) {
		return &fakeStrm{read: func(p []byte) (int, error) {
			p[0] = 'S'
			return 1, nil
		}}, nil
	}
	ms, spoke, err := probeStream(context.Background(), open, 2*time.Second)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !spoke {
		t.Fatal("want spoke=true for banner-first target")
	}
	if ms < 0 || ms > 2000 {
		t.Fatalf("ms out of range: %d", ms)
	}
}

func TestProbeStreamFastCloseMeansUnreachable(t *testing.T) {
	open := func(context.Context) (tnet.Strm, error) {
		return &fakeStrm{read: func([]byte) (int, error) { return 0, io.EOF }}, nil
	}
	_, _, err := probeStream(context.Background(), open, 2*time.Second)
	if err == nil {
		t.Fatal("want error for fast close, got nil")
	}
}

func TestProbeStreamHeldOpenMeansReachable(t *testing.T) {
	open := func(context.Context) (tnet.Strm, error) {
		return &fakeStrm{read: func([]byte) (int, error) {
			select {}
		}}, nil
	}
	start := time.Now()
	ms, spoke, err := probeStream(context.Background(), open, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("held stream must not error: %v", err)
	}
	if spoke {
		t.Fatal("want spoke=false for silent held stream")
	}
	if el := time.Since(start); el < 150*time.Millisecond {
		t.Fatalf("returned too fast (%v), should hold to the timeout", el)
	}
	if ms < 150 || ms > 5000 {
		t.Fatalf("ms out of range: %d", ms)
	}
}

func TestProbeStreamParentCancelAborts(t *testing.T) {
	open := func(context.Context) (tnet.Strm, error) {
		return &fakeStrm{read: func([]byte) (int, error) {
			select {}
		}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, _, err := probeStream(ctx, open, 5*time.Second)
	if err == nil {
		t.Fatal("want error on parent cancel, got nil")
	}
}
