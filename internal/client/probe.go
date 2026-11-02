package client

import (
	"context"
	"io"
	"time"

	"paqet/internal/tnet"
)

// probeStream opens one tunnel stream to target and classifies it:
//
//   - open fails            -> tunnel down / bad address (err)
//   - first read gets data   -> target spoke first, reachable (spoke=true)
//   - stream held with no data until the deadline -> server is holding it
//     open, which only happens while its dial to the target stands
//     (spoke=false, no error)
//   - fast EOF/close        -> the server gave up dialling the target (err)
//   - parent ctx ends first  -> aborted (ctx err)
//
// A plain TCP connect cannot prove target reachability here: the server
// dials the target asynchronously after the stream opens, so only the
// stream's subsequent behaviour carries the signal.
func probeStream(ctx context.Context, open func(context.Context) (tnet.Strm, error), timeout time.Duration) (ms int64, spoke bool, err error) {
	start := time.Now()
	strm, err := open(ctx)
	if err != nil {
		return 0, false, err
	}
	defer strm.Close()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	type result struct {
		n   int
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		buf := make([]byte, 1)
		n, rerr := strm.Read(buf)
		resCh <- result{n: n, err: rerr}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-resCh:
		el := time.Since(start).Milliseconds()
		if r.err == nil && r.n > 0 {
			return el, true, nil
		}
		if r.err == io.EOF || r.err == io.ErrUnexpectedEOF {
			return el, false, io.EOF
		}
		if r.err != nil {
			return el, false, r.err
		}
		return el, true, nil
	case <-timer.C:
		return time.Since(start).Milliseconds(), false, nil
	case <-ctx.Done():
		return time.Since(start).Milliseconds(), false, ctx.Err()
	}
}

// ProbeTarget probes target through the tunnel: stream open plus the
// read-hold classification from probeStream.
func (c *Client) ProbeTarget(ctx context.Context, target string, timeout time.Duration) (ms int64, spoke bool, err error) {
	return probeStream(ctx, func(ctx context.Context) (tnet.Strm, error) {
		return c.TCP(ctx, target)
	}, timeout)
}
