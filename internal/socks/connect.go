package socks

import (
	"context"
	"net"
	"sync/atomic"

	"paqet/internal/flog"
	"paqet/internal/metrics"
	"paqet/internal/pkg/buffer"
)

func (s *Server) handleConnect(ctx context.Context, conn net.Conn, req *request) {
	addr := req.address()
	strm, err := s.client.TCP(ctx, addr)
	if err != nil {
		flog.Errorf("SOCKS5 failed to establish TCP stream for %s -> %s: %v", conn.RemoteAddr(), addr, err)
		metrics.IncFailure()
		metrics.IncFailureTCP()
		metrics.IncFailureSOCKS()
		metrics.IncTarget(addr, 0, 0, false)
		s.write(conn, repFailure)
		return
	}
	defer strm.Close()
	flog.Infof("SOCKS5 accepted TCP connection %s -> %s", conn.RemoteAddr(), addr)

	lAddr := conn.LocalAddr().(*net.TCPAddr)
	if _, err := conn.Write(append([]byte{ver, repSuccess, 0x00}, putAddr(nil, lAddr.IP, lAddr.Port)...)); err != nil {
		metrics.IncFailure()
		metrics.IncFailureTCP()
		metrics.IncFailureSOCKS()
		metrics.IncTarget(addr, 0, 0, false)
		return
	}

	errCh := make(chan error, 2)
	var outN, inN atomic.Int64
	go func() { errCh <- buffer.CopyT(buffer.CountWriter{W: strm, N: &outN}, conn) }()
	go func() { errCh <- buffer.CopyT(buffer.CountWriter{W: conn, N: &inN}, strm) }()

	select {
	case err := <-errCh:
		// Best-effort snapshot: the other direction may still be copying;
		// its counter is read atomically. Caller closes conn on return,
		// which unblocks the peer CopyT (errCh is buffered so no leak).
		inBytes, outBytes := uint64(inN.Load()), uint64(outN.Load())
		if err != nil {
			flog.Errorf("SOCKS5 TCP stream %d failed for %s -> %s: %v", strm.SID(), conn.RemoteAddr(), addr, err)
			metrics.IncFailure()
			metrics.IncFailureTCP()
			metrics.IncFailureSOCKS()
			metrics.IncTarget(addr, inBytes, outBytes, false)
		} else {
			metrics.IncSuccess()
			metrics.IncSuccessTCP()
			metrics.IncSuccessSOCKS()
			metrics.IncTarget(addr, inBytes, outBytes, true)
		}
	case <-ctx.Done():
		// Don't drop byte totals on shutdown/drain.
		metrics.IncSuccess()
		metrics.IncSuccessTCP()
		metrics.IncSuccessSOCKS()
		metrics.IncTarget(addr, uint64(inN.Load()), uint64(outN.Load()), true)
	}

	flog.Debugf("SOCKS5 connection %s -> %s closed", conn.RemoteAddr(), addr)
}
