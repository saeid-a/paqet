package socks

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"time"

	"paqet/internal/client"
	"paqet/internal/conf"
	"paqet/internal/flog"
	"paqet/internal/metrics"
)

type Server struct {
	client   *client.Client
	username string
	password string
	// egressSpec is the configured exit: "" = direct (respect the app's
	// requested destination), otherwise a fixed "host:port". Dynamic
	// specs ("@active", "forward:<listen>") are resolved at dial time
	// via resolve, which the supervisor installs; resolve returning ""
	// falls back to direct.
	egressSpec atomic.Value // string
	resolveMu  atomic.Value // func() string
}

func (s *Server) SetEgress(spec string) { s.egressSpec.Store(spec) }

func (s *Server) Egress() string {
	if v, ok := s.egressSpec.Load().(string); ok {
		return v
	}
	return ""
}

// SetEgressSpec installs a (possibly dynamic) egress spec with a resolver
// for "@active"/"forward:" specs. A nil resolve means the spec is literal.
func (s *Server) SetEgressSpec(spec string, resolve func() string) {
	s.egressSpec.Store(spec)
	if resolve != nil {
		s.resolveMu.Store(resolve)
	}
}

// effectiveTarget returns the tunnel dial target for a SOCKS request:
// the egress override when one resolves, else the requested destination.
func (s *Server) effectiveTarget(requested string) string {
	spec := s.Egress()
	if spec == "" {
		return requested
	}
	if r, ok := s.resolveMu.Load().(func() string); ok && r != nil {
		if resolved := r(); resolved != "" {
			return resolved
		}
		// Unresolvable dynamic spec (no active forward): fall through to
		// the literal spec only when it is itself a dialable target.
		if spec == "@active" || (len(spec) > 8 && spec[:8] == "forward:") {
			return requested
		}
	}
	return spec
}

func New(client *client.Client) (*Server, error) {
	return &Server{client: client}, nil
}

func (s *Server) Start(ctx context.Context, cfg conf.SOCKS5) error {
	s.username, s.password = cfg.Username, cfg.Password

	listener, err := net.Listen("tcp", cfg.Listen.String())
	if err != nil {
		return err
	}
	flog.Infof("SOCKS5 server listening on %s", cfg.Listen.String())

	go s.serveTCP(ctx, listener)
	context.AfterFunc(ctx, func() { listener.Close() })

	return nil
}

func (s *Server) serveTCP(ctx context.Context, listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				metrics.IncFailure()
				metrics.IncFailureTCP()
				metrics.IncFailureSOCKS()
				continue
			}
		}

		go func() {
			defer conn.Close()
			s.handleTCPConn(ctx, conn)
		}()
	}
}

func (s *Server) handleTCPConn(ctx context.Context, conn net.Conn) {
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	if err := s.negotiate(conn); err != nil {
		flog.Debugf("SOCKS5 negotiation with %s failed: %v", conn.RemoteAddr(), err)
		metrics.IncFailure()
		metrics.IncFailureTCP()
		metrics.IncFailureSOCKS()
		return
	}
	req, err := s.read(conn)
	if err != nil {
		flog.Debugf("SOCKS5 request from %s failed: %v", conn.RemoteAddr(), err)
		metrics.IncFailure()
		metrics.IncFailureTCP()
		metrics.IncFailureSOCKS()
		return
	}
	conn.SetDeadline(time.Time{})

	switch req.cmd {
	case cmdConnect:
		flog.Debugf("SOCKS5 CONNECT from %s to %s", conn.RemoteAddr(), req.address())
		s.handleConnect(ctx, conn, req)
	case cmdUDP:
		flog.Debugf("SOCKS5 UDP_ASSOCIATE from %s", conn.RemoteAddr())
		s.handleAssociate(ctx, conn, req)
	default:
		flog.Debugf("SOCKS5 unsupported command %d from %s", req.cmd, conn.RemoteAddr())
		metrics.IncFailure()
		metrics.IncFailureTCP()
		metrics.IncFailureSOCKS()
		s.write(conn, repCmdUnsupp)
	}
}

func (s *Server) negotiate(conn net.Conn) error {
	var hdr [2]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return err
	}
	if hdr[0] != ver {
		return errProtocol
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}

	want := byte(methodNone)
	if s.username != "" || s.password != "" {
		want = methodAuth
	}
	for _, m := range methods {
		if m == want {
			if _, err := conn.Write([]byte{ver, want}); err != nil {
				return err
			}
			if want == methodAuth {
				return s.authenticate(conn)
			}
			return nil
		}
	}
	conn.Write([]byte{ver, methodNA})
	return errProtocol
}

func (s *Server) authenticate(conn net.Conn) error {
	var b [2]byte
	if _, err := io.ReadFull(conn, b[:]); err != nil { // VER + ULEN
		return err
	}
	if b[0] != verAuth {
		return errProtocol
	}
	user := make([]byte, b[1])
	if _, err := io.ReadFull(conn, user); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, b[1:]); err != nil { // PLEN
		return err
	}
	pass := make([]byte, b[1])
	if _, err := io.ReadFull(conn, pass); err != nil {
		return err
	}
	if string(user) == s.username && string(pass) == s.password {
		_, err := conn.Write([]byte{verAuth, repSuccess})
		return err
	}
	conn.Write([]byte{verAuth, repFailure})
	return errProtocol
}

func (s *Server) read(conn net.Conn) (*request, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil, err
	}
	if hdr[0] != ver {
		return nil, errProtocol
	}
	atyp, addr, port, err := readAddr(conn)
	if err != nil {
		return nil, err
	}
	return &request{cmd: hdr[1], atyp: atyp, addr: addr, port: port}, nil
}

func (s *Server) write(conn net.Conn, rep byte) error {
	_, err := conn.Write([]byte{ver, rep, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
