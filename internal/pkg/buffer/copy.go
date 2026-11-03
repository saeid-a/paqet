package buffer

import (
	"errors"
	"io"
	"sync/atomic"
)

const (
	TCPSize = 8 * 1024
	UDPSize = 4 * 1024
	_       = uint(0xFFFF - UDPSize)
)

func CopyT(dst io.Writer, src io.Reader) error {
	_, err := CopyTCount(dst, src)
	return err
}

// CopyTCount copies like CopyT and returns bytes written to dst.
func CopyTCount(dst io.Writer, src io.Reader) (int64, error) {
	var total int64
	buf := make([]byte, TCPSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
			if w < n {
				return total, io.ErrShortWrite
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}

func CopyU(dst io.Writer, src io.Reader) error {
	buf := make([]byte, UDPSize+1)
	for {
		n, err := src.Read(buf)
		switch {
		case n > UDPSize:
		case n > 0:
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// CountWriter wraps a Writer and atomically counts bytes successfully
// written. Shared by forward/socks/server TCP+UDP paths so byte totals
// for Targets stay consistent; complements CopyTCount (which returns a
// total for single-goroutine copies) for the two-goroutine bidirectional
// copy pattern where each direction needs its own concurrent counter.
type CountWriter struct {
	W io.Writer
	N *atomic.Int64
}

func (c CountWriter) Write(p []byte) (int, error) {
	n, err := c.W.Write(p)
	if n > 0 && c.N != nil {
		c.N.Add(int64(n))
	}
	return n, err
}
