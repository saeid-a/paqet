package run

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"paqet/internal/client"
	"paqet/internal/conf"
	"paqet/internal/forward"
	"paqet/internal/socks"
)

func startClient(cfg *conf.Conf) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var sup *supervisor
	sup = newSupervisor(confPath, func(coreCtx context.Context, c *conf.Conf) error {
		cl, err := client.New(c)
		if err != nil {
			return err
		}
		if err := cl.Start(coreCtx); err != nil {
			return err
		}
		if err := registerDataPlaneListeners(coreCtx, sup, cl, c); err != nil {
			return err
		}
		for _, name := range sup.ListListeners() {
			if err := sup.StartListener(name); err != nil {
				return err
			}
		}
		return nil
	})

	sup.boot(ctx, cfg)
	log.Printf("shutdown signal received, shutting down...")
}

// registerDataPlaneListeners registers each SOCKS5/forward data-plane
// listener with the supervisor so the panel can start/stop them. IDs are
// "socks5:<listen>" and "forward:<listen>" using the configured listen
// strings, matching the static entries the panel already shows.
func registerDataPlaneListeners(coreCtx context.Context, sup *supervisor, cl *client.Client, cfg *conf.Conf) error {
	for _, ss := range cfg.SOCKS5 {
		ss := ss
		s, err := socks.New(cl)
		if err != nil {
			return err
		}
		sup.RegisterListener("socks5:"+ss.Listen_, func(ctx context.Context) error {
			return serveUntilDone(ctx, coreCtx, func(c context.Context) error { return s.Start(c, ss) })
		})
	}
	for _, ff := range cfg.Forward {
		ff := ff
		f, err := forward.New(cl, ff.Listen.String(), ff.Target)
		if err != nil {
			return err
		}
		sup.RegisterListener("forward:"+ff.Listen_, func(ctx context.Context) error {
			return serveUntilDone(ctx, coreCtx, func(c context.Context) error { return f.Start(c, ff.Protocol) })
		})
	}
	return nil
}

// serveUntilDone runs start with a context cancelled by either parent,
// then blocks while serving so ListenerRunning stays true. Returning nil
// after serving avoids the supervisor retry loop; only start failures are
// returned. Tying the socket lifetime to coreCtx preserves the current
// behavior where listeners die with the core (stop/restart).
func serveUntilDone(ctx, coreCtx context.Context, start func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopCore := context.AfterFunc(coreCtx, cancel)
	defer stopCore()
	if err := start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
