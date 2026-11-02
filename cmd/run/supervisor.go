package run

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"paqet/cmd/version"
	"paqet/internal/conf"
	"paqet/internal/flog"
	"paqet/internal/metrics"
)

// supervisor owns the core lifecycle so the control panel stays alive
// while the proxy itself is stopped/started/restarted.
type supervisor struct {
	configPath string

	mu         sync.Mutex
	coreCtx    context.Context
	coreCancel context.CancelFunc
	running    atomic.Bool
	startFn    func(ctx context.Context, cfg *conf.Conf) error
}

func newSupervisor(configPath string, startFn func(ctx context.Context, cfg *conf.Conf) error) *supervisor {
	return &supervisor{configPath: configPath, startFn: startFn}
}

func (s *supervisor) isRunning() bool { return s.running.Load() }

func (s *supervisor) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running.Load() {
		return fmt.Errorf("already running")
	}
	cfg, err := conf.LoadFromFile(s.configPath)
	if err != nil {
		return fmt.Errorf("cannot start: %v", err)
	}
	flog.SetLevel(cfg.Log.Level)
	metrics.SetRole(cfg.Role)

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.startFn(ctx, cfg); err != nil {
		cancel()
		return err
	}
	s.coreCtx = ctx
	s.coreCancel = cancel
	s.running.Store(true)
	metrics.SetRunning(true)
	flog.Infof("core started via control panel")
	return nil
}

func (s *supervisor) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running.Load() {
		return fmt.Errorf("already stopped")
	}
	if s.coreCancel != nil {
		s.coreCancel()
	}
	s.coreCtx = nil
	s.coreCancel = nil
	s.running.Store(false)
	metrics.SetRunning(false)
	flog.Infof("core stopped via control panel")
	return nil
}

func (s *supervisor) restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running.Load() && s.coreCancel != nil {
		s.coreCancel()
	}
	s.coreCtx = nil
	s.coreCancel = nil
	s.running.Store(false)
	metrics.SetRunning(false)

	cfg, err := conf.LoadFromFile(s.configPath)
	if err != nil {
		return fmt.Errorf("cannot restart (bad config): %v", err)
	}
	flog.SetLevel(cfg.Log.Level)
	metrics.SetRole(cfg.Role)

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.startFn(ctx, cfg); err != nil {
		cancel()
		return err
	}
	s.coreCtx = ctx
	s.coreCancel = cancel
	s.running.Store(true)
	metrics.SetRunning(true)
	flog.Infof("core restarted via control panel")
	return nil
}

// boot wires the admin UI (parent ctx lifetime) and starts the core once.
// parentCtx is cancelled on SIGINT/SIGTERM.
func (s *supervisor) boot(parentCtx context.Context, initial *conf.Conf) {
	metrics.SetRole(initial.Role)
	metrics.SetConfigPath(s.configPath)
	metrics.SetRunning(false)
	metrics.SetBuildInfo(metrics.BuildInfo{
		Version:   version.Version,
		GitTag:    version.GitTag,
		GitCommit: version.GitCommit,
		BuildTime: version.BuildTime,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	})

	adminCfg := initial.EffectiveAdmin()
	metrics.StartWithController(parentCtx, adminCfg, metrics.Controller{
		ConfigPath: s.configPath,
		Running:    s.isRunning,
		Start:      s.start,
		Stop:       s.stop,
		Restart:    s.restart,
	})

	// initial start (same path as UI start, but without double-lock confusion)
	s.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	err := s.startFn(ctx, initial)
	if err != nil {
		s.mu.Unlock()
		cancel()
		flog.Errorf("core failed to start: %v", err)
	} else {
		s.coreCtx = ctx
		s.coreCancel = cancel
		s.running.Store(true)
		metrics.SetRunning(true)
		s.mu.Unlock()
	}

	<-parentCtx.Done()

	s.mu.Lock()
	if s.coreCancel != nil {
		s.coreCancel()
	}
	s.running.Store(false)
	metrics.SetRunning(false)
	s.mu.Unlock()
}
