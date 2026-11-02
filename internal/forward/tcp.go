package forward

import (
	"context"
	"net"
	"sync/atomic"

	"paqet/internal/flog"
	"paqet/internal/metrics"
	"paqet/internal/pkg/buffer"
)

func (f *Forward) serveTCP(ctx context.Context, listener net.Listener) {
	flog.Infof("TCP forwarder listening on %s -> %s", f.listenAddr, f.targetAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				metrics.IncFailure()
				metrics.IncFailureForwardTCP()
				metrics.IncFailureTCP()
				metrics.IncTarget(f.targetAddr, 0, 0, false)
				continue
			}
		}

		go func() {
			defer conn.Close()
			f.handleTCPConn(ctx, conn)
		}()
	}
}

func (f *Forward) handleTCPConn(ctx context.Context, conn net.Conn) {
	strm, err := f.client.TCP(ctx, f.targetAddr)
	if err != nil {
		flog.Errorf("failed to establish stream for %s -> %s: %v", conn.RemoteAddr(), f.targetAddr, err)
		metrics.IncFailure()
		metrics.IncFailureForwardTCP()
		metrics.IncFailureTCP()
		metrics.IncTarget(f.targetAddr, 0, 0, false)
		return
	}
	defer strm.Close()
	flog.Infof("accepted TCP connection %s -> %s", conn.RemoteAddr(), f.targetAddr)

	// Count bytes per direction so Targets shows real totals instead of 0.
	// outN: client -> target, inN: target -> client.
	var outN, inN atomic.Int64
	errCh := make(chan error, 2)
	go func() { errCh <- buffer.CopyT(buffer.CountWriter{W: strm, N: &outN}, conn) }()
	go func() { errCh <- buffer.CopyT(buffer.CountWriter{W: conn, N: &inN}, strm) }()

	select {
	case err := <-errCh:
		// Best-effort snapshot: the other direction may still be copying;
		// its counter is read atomically. Caller closes conn on return,
		// which unblocks the peer CopyT (errCh is buffered so no leak).
		inBytes, outBytes := uint64(inN.Load()), uint64(outN.Load())
		if err != nil {
			flog.Errorf("TCP stream %d failed for %s -> %s: %v", strm.SID(), conn.RemoteAddr(), f.targetAddr, err)
			metrics.IncFailure()
			metrics.IncFailureForwardTCP()
			metrics.IncFailureTCP()
			metrics.IncTarget(f.targetAddr, inBytes, outBytes, false)
		} else {
			metrics.IncSuccess()
			metrics.IncSuccessForwardTCP()
			metrics.IncSuccessTCP()
			metrics.IncTarget(f.targetAddr, inBytes, outBytes, true)
		}
	case <-ctx.Done():
		// Don't drop byte totals on shutdown/drain.
		inBytes, outBytes := uint64(inN.Load()), uint64(outN.Load())
		metrics.IncSuccess()
		metrics.IncSuccessForwardTCP()
		metrics.IncSuccessTCP()
		metrics.IncTarget(f.targetAddr, inBytes, outBytes, true)
	}
}
