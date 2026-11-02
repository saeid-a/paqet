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

	sup := newSupervisor(confPath, func(coreCtx context.Context, c *conf.Conf) error {
		cl, err := client.New(c)
		if err != nil {
			return err
		}
		if err := cl.Start(coreCtx); err != nil {
			return err
		}
		for _, ss := range c.SOCKS5 {
			ss := ss
			s, err := socks.New(cl)
			if err != nil {
				return err
			}
			if err := s.Start(coreCtx, ss); err != nil {
				return err
			}
		}
		for _, ff := range c.Forward {
			ff := ff
			f, err := forward.New(cl, ff.Listen.String(), ff.Target)
			if err != nil {
				return err
			}
			if err := f.Start(coreCtx, ff.Protocol); err != nil {
				return err
			}
		}
		return nil
	})

	sup.boot(ctx, cfg)
	log.Printf("shutdown signal received, shutting down...")
}
