package run

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"paqet/cmd/version"
	"paqet/internal/conf"
	"paqet/internal/flog"
	"paqet/internal/metrics"
)

const (
	// supervisorMaxAttempts caps core/listener start attempts when
	// auto-restart is enabled (one initial attempt plus retries).
	supervisorMaxAttempts = 5
	// supervisorRetryDelay is the backoff between start attempts.
	supervisorRetryDelay = 2 * time.Second
	// drainPollInterval is how often drain() re-checks active connections.
	drainPollInterval = 200 * time.Millisecond
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
	// startCancel cancels an in-flight start()/restart() including its
	// retry backoff, so stop() during backoff actually stops. startGen
	// guards against an orphaned start clearing a newer attempt's cancel.
	// Both are guarded by mu.
	startCancel context.CancelFunc
	startGen    uint64

	// autoRestart enables retry-with-backoff on core start failures and
	// on registered listener failures. Off by default to preserve the
	// original single-attempt behaviour; flip via SetAutoRestart.
	autoRestart atomic.Bool

	listenerMu     sync.Mutex
	listeners      map[string]func(context.Context) error
	listenerCancel map[string]context.CancelFunc
}

func newSupervisor(configPath string, startFn func(ctx context.Context, cfg *conf.Conf) error) *supervisor {
	return &supervisor{
		configPath:     configPath,
		startFn:        startFn,
		listeners:      make(map[string]func(context.Context) error),
		listenerCancel: make(map[string]context.CancelFunc),
	}
}

func (s *supervisor) isRunning() bool { return s.running.Load() }

// SetAutoRestart toggles retry-with-backoff on core/listener failures.
func (s *supervisor) SetAutoRestart(v bool) { s.autoRestart.Store(v) }

// AutoRestartEnabled reports whether retry-with-backoff is on.
func (s *supervisor) AutoRestartEnabled() bool { return s.autoRestart.Load() }

// maxAttempts returns 1 normally, supervisorMaxAttempts with auto-restart.
func (s *supervisor) maxAttempts() int {
	if s.autoRestart.Load() {
		return supervisorMaxAttempts
	}
	return 1
}

// runStartLocked invokes startFn with backoff. s.mu must be held on entry
// and is still held on return; it is released while sleeping between
// attempts so stop/status paths stay responsive. If the core context is
// cancelled while backing off, the retry loop aborts early.
func (s *supervisor) runStartLocked(ctx context.Context, cfg *conf.Conf) error {
	attempts := s.maxAttempts()
	var err error
	for i := 0; i < attempts; i++ {
		err = s.startFn(ctx, cfg)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		flog.Errorf("core start failed (attempt %d/%d): %v", i+1, attempts, err)
		if i+1 >= attempts {
			break
		}
		s.mu.Unlock()
		timer := time.NewTimer(supervisorRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.mu.Lock()
			return err
		case <-timer.C:
		}
		s.mu.Lock()
		// If another request started the core while we were backing off,
		// do not clobber its state.
		if s.running.Load() {
			return fmt.Errorf("core started by another request, aborting retry")
		}
	}
	return err
}

func (s *supervisor) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running.Load() {
		return fmt.Errorf("already running")
	}
	if s.startCancel != nil {
		return fmt.Errorf("start already in progress")
	}
	cfg, err := conf.LoadFromFile(s.configPath)
	if err != nil {
		return fmt.Errorf("cannot start: %v", err)
	}
	flog.SetLevel(cfg.Log.Level)
	metrics.SetRole(cfg.Role)

	ctx, cancel := context.WithCancel(context.Background())
	s.startGen++
	gen := s.startGen
	s.startCancel = cancel
	if err := s.runStartLocked(ctx, cfg); err != nil {
		if s.startGen == gen {
			s.startCancel = nil
		}
		cancel()
		return err
	}
	if s.startGen == gen {
		s.startCancel = nil
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
	if s.startCancel != nil {
		// A start (or its retry backoff) is in flight: cancel it so the
		// core never comes up after this stop returns.
		s.startCancel()
		s.startGen++
		s.startCancel = nil
		metrics.SetRunning(false)
		flog.Infof("core start cancelled via control panel")
		return nil
	}
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

	if s.startCancel != nil {
		// Cancel an in-flight start; the restart below supersedes it.
		s.startCancel()
		s.startGen++
		s.startCancel = nil
	}
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
	s.startGen++
	gen := s.startGen
	s.startCancel = cancel
	if err := s.runStartLocked(ctx, cfg); err != nil {
		if s.startGen == gen {
			s.startCancel = nil
		}
		cancel()
		return err
	}
	if s.startGen == gen {
		s.startCancel = nil
	}
	s.coreCtx = ctx
	s.coreCancel = cancel
	s.running.Store(true)
	metrics.SetRunning(true)
	flog.Infof("core restarted via control panel")
	return nil
}

// RegisterListener adds a named long-running listener func to the registry.
// The func is not started until StartListener is called. Re-registering a
// running listener is refused; stop it first.
func (s *supervisor) RegisterListener(name string, fn func(context.Context) error) {
	if name == "" || fn == nil {
		return
	}
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	if _, running := s.listenerCancel[name]; running {
		flog.Errorf("cannot re-register running listener %q", name)
		return
	}
	s.listeners[name] = fn
}

// UnregisterListener removes a listener; a running listener is stopped first.
func (s *supervisor) UnregisterListener(name string) {
	s.listenerMu.Lock()
	cancel, running := s.listenerCancel[name]
	if running {
		delete(s.listenerCancel, name)
	}
	delete(s.listeners, name)
	s.listenerMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ListListeners returns the registered listener names in sorted order.
func (s *supervisor) ListListeners() []string {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	names := make([]string, 0, len(s.listeners))
	for name := range s.listeners {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListenerRunning reports whether the named listener is currently started.
func (s *supervisor) ListenerRunning(name string) bool {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	_, ok := s.listenerCancel[name]
	return ok
}

// StartListener starts a registered listener in the background.
func (s *supervisor) StartListener(name string) error {
	s.listenerMu.Lock()
	fn, ok := s.listeners[name]
	if !ok {
		s.listenerMu.Unlock()
		return fmt.Errorf("unknown listener %q", name)
	}
	if _, running := s.listenerCancel[name]; running {
		s.listenerMu.Unlock()
		return fmt.Errorf("listener %q already running", name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.listenerCancel[name] = cancel
	s.listenerMu.Unlock()
	go s.runListener(name, ctx, fn)
	return nil
}

// StopListener stops a running listener.
func (s *supervisor) StopListener(name string) error {
	s.listenerMu.Lock()
	cancel, ok := s.listenerCancel[name]
	if !ok {
		s.listenerMu.Unlock()
		return fmt.Errorf("listener %q not running", name)
	}
	delete(s.listenerCancel, name)
	s.listenerMu.Unlock()
	cancel()
	return nil
}

// runListener executes a listener func with backoff when auto-restart is on.
// An intentional stop (context cancelled) is not treated as a failure.
func (s *supervisor) runListener(name string, ctx context.Context, fn func(context.Context) error) {
	attempts := s.maxAttempts()
	for i := 0; i < attempts; i++ {
		err := fn(ctx)
		if err == nil || ctx.Err() != nil {
			s.finishListener(name)
			return
		}
		flog.Errorf("listener %q failed (attempt %d/%d): %v", name, i+1, attempts, err)
		if i+1 >= attempts {
			flog.Errorf("listener %q gave up after %d attempts", name, attempts)
			s.finishListener(name)
			return
		}
		timer := time.NewTimer(supervisorRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.finishListener(name)
			return
		case <-timer.C:
		}
	}
}

// finishListener clears the running marker for a listener that exited.
// Idempotent: StopListener already removes the marker on the stop path.
func (s *supervisor) finishListener(name string) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	delete(s.listenerCancel, name)
}

// Drain closes all supervisor-registered listeners and waits for active
// connections and streams to drop to zero, or until timeoutSec elapses.
// NOTE: SOCKS5/forward data-plane listeners are not yet registered in the
// supervisor (see listenersHandler), so Drain currently only stops
// registered listeners; new data-plane accepts may keep arriving and cause
// a timeout. The core itself is left running so in-flight work can finish;
// call stop() afterwards for a full shutdown. A non-positive timeoutSec
// selects a 10s default.
func (s *supervisor) Drain(timeoutSec int) error {
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	s.listenerMu.Lock()
	for name := range s.listenerCancel {
		s.listenerCancel[name]()
		delete(s.listenerCancel, name)
	}
	s.listenerMu.Unlock()
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		snap := metrics.SnapshotNow()
		if snap.ActiveConnections == 0 && snap.ActiveStreams == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("drain timed out after %ds (%d connections, %d streams still active)",
				timeoutSec, snap.ActiveConnections, snap.ActiveStreams)
		}
		time.Sleep(drainPollInterval)
	}
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
		ConfigPath:      s.configPath,
		Running:         s.isRunning,
		Start:           s.start,
		Stop:            s.stop,
		Restart:         s.restart,
		Listeners:       s.ListListeners,
		ListenerStart:   s.StartListener,
		ListenerStop:    s.StopListener,
		ListenerRunning: s.ListenerRunning,
		Drain:           s.Drain,
	})

	// initial start (same path as UI start, but without double-lock confusion).
	// The retry ctx derives from parentCtx so SIGINT aborts a backoff sleep.
	s.mu.Lock()
	ctx, cancel := context.WithCancel(parentCtx)
	s.startGen++
	gen := s.startGen
	s.startCancel = cancel
	err := s.runStartLocked(ctx, initial)
	if s.startGen == gen {
		s.startCancel = nil
	}
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
