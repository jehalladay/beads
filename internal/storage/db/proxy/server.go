package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
	"golang.org/x/sync/errgroup"

	"github.com/steveyegge/beads/internal/storage/db/server"
)

type ProxyOpts struct {
	RootDir     string
	Port        int
	IdleTimeout time.Duration
	Server      server.DatabaseServer
	// Stats is optional. When non-nil, the proxy records per-event counters
	// against it; tests use Snapshot() to assert. Production code should
	// leave this nil.
	Stats *Stats
}

type proxyServer struct {
	rootDir     string
	port        int
	idleTimeout time.Duration
	server      server.DatabaseServer
	stats       *Stats

	listener    net.Listener
	activeConns atomic.Int64
	conns       errgroup.Group
}

const (
	serverReadyTimeout     = 30 * time.Second
	readyPingTimeout       = 2 * time.Second
	readyInitialBackoff    = 50 * time.Millisecond
	readyMaxBackoff        = 1 * time.Second
	idleWatcherMinInterval = 1 * time.Second
)

var (
	errIdleTimeout    = errors.New("idle timeout reached")
	errSignalReceived = errors.New("signal received")
)

func NewProxyServer(opts ProxyOpts) *proxyServer {
	return &proxyServer{
		rootDir:     opts.RootDir,
		port:        opts.Port,
		idleTimeout: opts.IdleTimeout,
		server:      opts.Server,
		stats:       opts.Stats,
	}
}

func (p *proxyServer) Start(ctx context.Context) error {
	addr := fmt.Sprintf("127.0.0.1:%d", p.port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	p.listener = ln
	defer ln.Close()
	p.stats.IncStart()

	if err := WriteDatabaseProxyPidFile(p.rootDir, PidFile{Pid: os.Getpid(), Port: p.port}); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer RemoveDatabaseProxyPidFile(p.rootDir)

	p.stats.IncBackendStart()
	if err := p.server.Start(); err != nil {
		return fmt.Errorf("start database server: %w", err)
	}

	if err := waitForServerReady(ctx, p.server, serverReadyTimeout); err != nil {
		p.stats.IncBackendStop()
		_ = p.server.Stop()
		return fmt.Errorf("database server not ready: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		<-gctx.Done()
		_ = p.listener.Close()
		return nil
	})
	g.Go(func() error { return p.idleWatcher(gctx) })
	g.Go(func() error { return p.signalHandler(gctx) })
	g.Go(func() error { return p.acceptLoop(gctx) })

	runErr := g.Wait()
	_ = p.conns.Wait()
	p.stats.IncBackendStop()
	if stopErr := p.server.Stop(); stopErr != nil && runErr == nil {
		runErr = fmt.Errorf("stop database server: %w", stopErr)
	}
	if errors.Is(runErr, errIdleTimeout) || errors.Is(runErr, errSignalReceived) {
		return nil
	}
	return runErr
}

func (p *proxyServer) signalHandler(ctx context.Context) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	select {
	case <-ctx.Done():
		return nil
	case <-sigCh:
		p.stats.IncSignalReceived()
		return errSignalReceived
	}
}

func (p *proxyServer) idleWatcher(ctx context.Context) error {
	if p.idleTimeout <= 0 {
		<-ctx.Done()
		return nil
	}
	interval := p.idleTimeout / 4
	if interval < idleWatcherMinInterval {
		interval = idleWatcherMinInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var idleSince time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if p.activeConns.Load() > 0 {
				idleSince = time.Time{}
				continue
			}
			if idleSince.IsZero() {
				idleSince = time.Now()
				continue
			}
			if time.Since(idleSince) >= p.idleTimeout {
				p.stats.IncIdleTimeout()
				return errIdleTimeout
			}
		}
	}
}

func (p *proxyServer) acceptLoop(ctx context.Context) error {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			p.stats.IncAcceptError()
			continue
		}
		p.stats.IncAccept()
		p.conns.Go(func() error {
			return p.handleConn(ctx, conn)
		})
	}
}

func (p *proxyServer) handleConn(ctx context.Context, client net.Conn) error {
	p.activeConns.Add(1)
	defer p.activeConns.Add(-1)

	p.stats.IncBackendDialAttempt()
	backend, err := p.server.Dial(ctx)
	if err != nil {
		p.stats.IncBackendDialError()
		_ = client.Close()
		return err
	}
	p.stats.IncBackendDialSuccess()
	p.stats.IncHandledConn()

	done := make(chan struct{})
	var doneOnce sync.Once
	finish := func() { doneOnce.Do(func() { close(done) }) }

	var g errgroup.Group
	g.Go(func() error {
		select {
		case <-ctx.Done():
			_ = client.Close()
			_ = backend.Close()
		case <-done:
		}
		return nil
	})
	g.Go(func() error {
		defer finish()
		defer backend.Close()
		defer client.Close()
		n, err := io.Copy(backend, client)
		p.stats.AddBytesClientToBackend(n)
		return err
	})
	g.Go(func() error {
		defer finish()
		defer backend.Close()
		defer client.Close()
		n, err := io.Copy(client, backend)
		p.stats.AddBytesBackendToClient(n)
		return err
	})
	return g.Wait()
}

func waitForServerReady(ctx context.Context, s server.DatabaseServer, timeout time.Duration) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = readyInitialBackoff
	bo.MaxInterval = readyMaxBackoff
	bo.MaxElapsedTime = timeout

	return backoff.Retry(func() error {
		if !s.Running() {
			return errors.New("database server not running")
		}
		pingCtx, cancel := context.WithTimeout(ctx, readyPingTimeout)
		defer cancel()
		return s.Ping(pingCtx)
	}, backoff.WithContext(bo, ctx))
}
