package run

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"paqet/internal/conf"
	"paqet/internal/server"
)

func startServer(cfg *conf.Conf) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sup := newSupervisor(confPath, func(coreCtx context.Context, c *conf.Conf) error {
		srv, err := server.New(c)
		if err != nil {
			return err
		}
		if err := srv.Start(coreCtx); err != nil {
			return err
		}
		return nil
	})

	sup.boot(ctx, cfg)
	log.Printf("shutdown signal received, shutting down...")
}
