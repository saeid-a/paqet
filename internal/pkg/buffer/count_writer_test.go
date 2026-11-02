package buffer

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCountWriterTracksCopyT(t *testing.T) {
	src := strings.NewReader("hello world hello world")
	var dst bytes.Buffer
	var n atomic.Int64
	cw := CountWriter{W: &dst, N: &n}
	if err := CopyT(cw, src); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != int64(dst.Len()) || got != 23 {
		t.Fatalf("want 23 counted, got %d len=%d", got, dst.Len())
	}
}

func TestCountWriterTracksCopyU(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 100)
	src := bytes.NewReader(payload)
	var dst bytes.Buffer
	var n atomic.Int64
	cw := CountWriter{W: &dst, N: &n}
	if err := CopyU(cw, src); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 100 {
		t.Fatalf("want 100 counted, got %d", got)
	}
}

func TestCountWriterNilCounterSafe(t *testing.T) {
	var dst bytes.Buffer
	cw := CountWriter{W: &dst}
	if _, err := cw.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if dst.String() != "hi" {
		t.Fatalf("want hi, got %q", dst.String())
	}
}
