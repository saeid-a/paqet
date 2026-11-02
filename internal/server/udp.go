package server

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"paqet/internal/flog"
	"paqet/internal/metrics"
	"paqet/internal/pkg/buffer"
	"paqet/internal/protocol"
	"paqet/internal/tnet"
)

func (s *Server) handleUDPProtocol(ctx context.Context, strm tnet.Strm, p *protocol.Proto) {
	flog.Infof("accepted UDP stream %d: %s -> %s", strm.SID(), strm.RemoteAddr(), p.Addr.String())
	s.handleUDP(ctx, strm, p.Addr.String())
}

func (s *Server) handleUDP(ctx context.Context, strm tnet.Strm, addr string) {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		flog.Errorf("failed to establish UDP connection to %s for stream %d: %v", addr, strm.SID(), err)
		metrics.IncFailure()
		metrics.IncFailureUDP()
		metrics.IncTarget(addr, 0, 0, false)
		return
	}
	defer func() {
		conn.Close()
		flog.Debugf("closed UDP connection %s for stream %d", addr, strm.SID())
	}()

	errChan := make(chan error, 2)
	var outN, inN atomic.Int64
	go func() { errChan <- buffer.CopyU(buffer.CountWriter{W: conn, N: &outN}, strm) }()
	go func() { errChan <- buffer.CopyU(buffer.CountWriter{W: strm, N: &inN}, conn) }()

	select {
	case err := <-errChan:
		inBytes, outBytes := uint64(inN.Load()), uint64(outN.Load())
		if err != nil {
			flog.Errorf("UDP stream %d failed for %s -> %s: %v", strm.SID(), conn.RemoteAddr(), addr, err)
			metrics.IncFailure()
			metrics.IncFailureUDP()
			metrics.IncTarget(addr, inBytes, outBytes, false)
			return
		}
	case <-ctx.Done():
		inBytes, outBytes := uint64(inN.Load()), uint64(outN.Load())
		metrics.IncSuccess()
		metrics.IncSuccessUDP()
		metrics.IncTarget(addr, inBytes, outBytes, true)
		return
	}

	metrics.IncSuccess()
	metrics.IncSuccessUDP()
	metrics.IncTarget(addr, uint64(inN.Load()), uint64(outN.Load()), true)
}
