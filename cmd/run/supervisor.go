package run

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"paqet/cmd/version"
	"paqet/internal/conf"
	"paqet/internal/flog"
	"paqet/internal/metrics"
	"paqet/internal/socks"
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
	listenerToken  map[string]uint64
	listenerSeq    uint64
	// socksServers tracks live SOCKS data-plane servers by listener ID
	// ("socks5:<listen>") so the panel can steer their egress at runtime.
	// socksEgress holds the configured spec per ID; forwardTargets maps
	// forward listen -> target from the running core's config for
	// "@active"/"forward:" resolution. All guarded by listenerMu.
	socksServers   map[string]*socks.Server
	socksEgress    map[string]string
	forwardTargets map[string]string
	// tunnelProbe dials targets through the tunnel for the panel's
	// through-tunnel probe. Set by the client startFn once the client
	// exists; nil when the core never started (e.g. server role).
	tunnelMu    sync.RWMutex
	tunnelProbe func(ctx context.Context, target string, timeout time.Duration) (ms int64, spoke bool, err error)
}

// setTunnelProbe installs the through-tunnel dial func for the panel.
func (s *supervisor) setTunnelProbe(fn func(ctx context.Context, target string, timeout time.Duration) (int64, bool, error)) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	s.tunnelProbe = fn
}

// probeViaTunnel runs the through-tunnel probe or reports it unavailable.
func (s *supervisor) probeViaTunnel(ctx context.Context, target string, timeoutSec int) (int64, bool, error) {
	s.tunnelMu.RLock()
	fn := s.tunnelProbe
	s.tunnelMu.RUnlock()
	if fn == nil {
		return 0, false, fmt.Errorf("tunnel probe unavailable (core not running)")
	}
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	if timeoutSec > 15 {
		timeoutSec = 15
	}
	return fn(ctx, target, time.Duration(timeoutSec)*time.Second)
}

func newSupervisor(configPath string, startFn func(ctx context.Context, cfg *conf.Conf) error) *supervisor {
	return &supervisor{
		configPath:     configPath,
		startFn:        startFn,
		listeners:      make(map[string]func(context.Context) error),
		listenerCancel: make(map[string]context.CancelFunc),
		listenerToken:  make(map[string]uint64),
		socksServers:   make(map[string]*socks.Server),
		socksEgress:    make(map[string]string),
		forwardTargets: make(map[string]string),
	}
}

func (s *supervisor) isRunning() bool { return s.running.Load() }

// trackSocksServer records a SOCKS data-plane server and its boot egress
// spec. The resolver closes over the spec so "@active"/"forward:" specs
// follow forward switches at dial time with no per-dial disk reads.
func (s *supervisor) trackSocksServer(id string, srv *socks.Server, spec string) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	if srv != nil {
		specCopy := spec
		srv.SetEgressSpec(spec, func() string { return s.resolveSocksEgress(specCopy) })
		s.socksServers[id] = srv
	}
	if _, ok := s.socksEgress[id]; !ok {
		s.socksEgress[id] = spec
	}
}

// setForwardTargets caches forward listen -> target from the running
// core's config for SOCKS egress resolution.
func (s *supervisor) setForwardTargets(m map[string]string) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	s.forwardTargets = m
}

// resolveSocksEgress maps an egress spec to a concrete dial target.
// "" means direct (no override). Unresolvable dynamic specs return ""
// and the SOCKS server falls back to the requested destination.
func (s *supervisor) resolveSocksEgress(spec string) string {
	switch {
	case spec == "":
		return ""
	case spec == "@active":
		return s.activeForwardTarget()
	case len(spec) > len("forward:") && spec[:len("forward:")] == "forward:":
		s.listenerMu.Lock()
		defer s.listenerMu.Unlock()
		return s.forwardTargets[spec[len("forward:"):]]
	default:
		return spec
	}
}

// activeForwardTarget returns the target of the sole running forward, or
// "" when zero or several forwards run (ambiguous which is active).
func (s *supervisor) activeForwardTarget() string {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	var found string
	count := 0
	for name := range s.listenerCancel {
		if len(name) <= len("forward:") || name[:len("forward:")] != "forward:" {
			continue
		}
		count++
		found = s.forwardTargets[name[len("forward:"):]]
	}
	if count != 1 {
		return ""
	}
	return found
}

// hasSocksListener reports whether id is a tracked SOCKS listener.
func (s *supervisor) hasSocksListener(id string) bool {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	_, ok := s.socksEgress[id]
	return ok
}

// SetSocksEgress steers a SOCKS listener's egress at runtime. The spec is
// validated ("", "@active", "forward:<listen>", or host:port); dynamic
// specs re-resolve on every dial so forward switches are followed.
func (s *supervisor) SetSocksEgress(id, spec string) error {
	if err := conf.ValidateSocksEgress(spec); err != nil {
		return err
	}
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	srv, ok := s.socksServers[id]
	if !ok {
		if _, known := s.socksEgress[id]; !known {
			return fmt.Errorf("unknown socks listener %q", id)
		}
		s.socksEgress[id] = spec
		return nil
	}
	specCopy := spec
	srv.SetEgressSpec(spec, func() string { return s.resolveSocksEgress(specCopy) })
	s.socksEgress[id] = spec
	return nil
}

// SocksEgress returns the configured spec and its currently resolved
// concrete target ("" = direct) for a SOCKS listener.
func (s *supervisor) SocksEgress(id string) (spec, resolved string) {
	s.listenerMu.Lock()
	spec = s.socksEgress[id]
	s.listenerMu.Unlock()
	return spec, s.resolveSocksEgress(spec)
}

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
//
// Each attempt runs with its own child context derived from ctx; the child
// is cancelled immediately when that attempt fails (including before retry
// backoff) so partially-started components tied to the attempt are torn
// down. On success the attempt's context and cancel func are returned so
// callers can retain them as coreCtx/coreCancel. On overall failure the
// caller retains responsibility for cleaning up the parent ctx.
func (s *supervisor) runStartLocked(ctx context.Context, cfg *conf.Conf) (context.Context, context.CancelFunc, error) {
	attempts := s.maxAttempts()
	var err error
	for i := 0; i < attempts; i++ {
		attemptCtx, attemptCancel := context.WithCancel(ctx)
		err = s.startFn(attemptCtx, cfg)
		if err == nil {
			return attemptCtx, attemptCancel, nil
		}
		attemptCancel()
		if ctx.Err() != nil {
			return nil, nil, err
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
			return nil, nil, err
		case <-timer.C:
		}
		s.mu.Lock()
		if ctx.Err() != nil {
			return nil, nil, err
		}
		// If another request started the core while we were backing off,
		// do not clobber its state.
		if s.running.Load() {
			return nil, nil, fmt.Errorf("core started by another request, aborting retry")
		}
	}
	return nil, nil, err
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
	attemptCtx, attemptCancel, runErr := s.runStartLocked(ctx, cfg)
	if runErr != nil {
		if s.startGen == gen {
			s.startCancel = nil
		}
		cancel()
		return runErr
	}
	if s.startGen != gen || ctx.Err() != nil || attemptCtx.Err() != nil {
		attemptCancel()
		if s.startGen == gen {
			s.startCancel = nil
			cancel()
		}
		if ctx.Err() != nil {
			return fmt.Errorf("core start aborted: %w", ctx.Err())
		}
		return fmt.Errorf("core start superseded, aborting commit")
	}
	if s.startGen == gen {
		s.startCancel = nil
	}
	s.coreCtx = attemptCtx
	s.coreCancel = func() { attemptCancel(); cancel() }
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

	cfg, err := conf.LoadFromFile(s.configPath)
	if err != nil {
		return fmt.Errorf("cannot restart (bad config): %v", err)
	}

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

	flog.SetLevel(cfg.Log.Level)
	metrics.SetRole(cfg.Role)

	ctx, cancel := context.WithCancel(context.Background())
	s.startGen++
	gen := s.startGen
	s.startCancel = cancel
	attemptCtx, attemptCancel, runErr := s.runStartLocked(ctx, cfg)
	if runErr != nil {
		if s.startGen == gen {
			s.startCancel = nil
		}
		cancel()
		return runErr
	}
	if s.startGen != gen || ctx.Err() != nil || attemptCtx.Err() != nil {
		attemptCancel()
		if s.startGen == gen {
			s.startCancel = nil
			cancel()
		}
		if ctx.Err() != nil {
			return fmt.Errorf("core restart aborted: %w", ctx.Err())
		}
		return fmt.Errorf("core restart superseded, aborting commit")
	}
	if s.startGen == gen {
		s.startCancel = nil
	}
	s.coreCtx = attemptCtx
	s.coreCancel = func() { attemptCancel(); cancel() }
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
		delete(s.listenerToken, name)
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
	s.listenerSeq++
	token := s.listenerSeq
	s.listenerCancel[name] = cancel
	s.listenerToken[name] = token
	s.listenerMu.Unlock()
	go s.runListener(name, ctx, fn, token)
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
	delete(s.listenerToken, name)
	s.listenerMu.Unlock()
	cancel()
	return nil
}

// runListener executes a listener func with backoff when auto-restart is on.
// An intentional stop (context cancelled) is not treated as a failure.
func (s *supervisor) runListener(name string, ctx context.Context, fn func(context.Context) error, token uint64) {
	attempts := s.maxAttempts()
	for i := 0; i < attempts; i++ {
		err := fn(ctx)
		if err == nil || ctx.Err() != nil {
			s.finishListener(name, token)
			return
		}
		flog.Errorf("listener %q failed (attempt %d/%d): %v", name, i+1, attempts, err)
		if i+1 >= attempts {
			flog.Errorf("listener %q gave up after %d attempts", name, attempts)
			s.finishListener(name, token)
			return
		}
		timer := time.NewTimer(supervisorRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.finishListener(name, token)
			return
		case <-timer.C:
		}
	}
}

// finishListener clears the running marker for a listener that exited.
// Idempotent: StopListener already removes the marker on the stop path.
// The token guards against an older run clearing a newer run's marker:
// only delete when the stored token matches the exiting run.
func (s *supervisor) finishListener(name string, token uint64) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	if s.listenerToken[name] != token {
		return
	}
	delete(s.listenerCancel, name)
	delete(s.listenerToken, name)
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
		delete(s.listenerToken, name)
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
		TunnelProbe:     s.probeViaTunnel,
		ListenerEgressSet: func(ctx context.Context, id, egress string) error {
			return s.SetSocksEgress(id, egress)
		},
		ListenerEgressGet: func(id string) (string, string, bool) {
			if !s.hasSocksListener(id) {
				return "", "", false
			}
			spec, resolved := s.SocksEgress(id)
			return spec, resolved, true
		},
	})

	// initial start (same path as UI start, but without double-lock confusion).
	// The retry ctx derives from parentCtx so SIGINT aborts a backoff sleep.
	s.mu.Lock()
	ctx, cancel := context.WithCancel(parentCtx)
	s.startGen++
	gen := s.startGen
	s.startCancel = cancel
	attemptCtx, attemptCancel, err := s.runStartLocked(ctx, initial)
	if s.startGen == gen {
		s.startCancel = nil
	}
	if err != nil {
		s.mu.Unlock()
		cancel()
		flog.Errorf("core failed to start: %v", err)
		if !adminCfg.Enabled {
			os.Exit(1)
		}
	} else {
		if s.startGen != gen || ctx.Err() != nil || attemptCtx.Err() != nil {
			attemptCancel()
			cancel()
			s.mu.Unlock()
			flog.Errorf("core start aborted before commit")
		} else {
			s.coreCtx = attemptCtx
			s.coreCancel = func() { attemptCancel(); cancel() }
			s.running.Store(true)
			metrics.SetRunning(true)
			s.mu.Unlock()
		}
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
