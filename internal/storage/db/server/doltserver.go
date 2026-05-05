package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dolthub/dolt/go/libraries/doltcore/servercfg"
	"github.com/dolthub/dolt/go/libraries/utils/filesys"
	"golang.org/x/sync/errgroup"

	"github.com/steveyegge/beads/internal/storage/db/util"
)

const defaultKeepAlivePeriod = 30 * time.Second

type DoltServer struct {
	id              string
	doltBinExec     string
	rootDir         string
	configPath      string
	config          servercfg.ServerConfig
	keepAlivePeriod time.Duration

	cmd     *exec.Cmd
	logFile *os.File
	eg      *errgroup.Group
	egCtx   context.Context
	cancel  context.CancelFunc
}

var _ DatabaseServer = (*DoltServer)(nil)

func NewDoltServer(doltBinExec, rootDir, configPath, logFilePath string, keepAlivePeriod time.Duration) (*DoltServer, error) {
	if doltBinExec == "" {
		return nil, errors.New("server: NewDoltServer: doltBinExec is required")
	}
	if rootDir == "" {
		return nil, errors.New("server: NewDoltServer: rootDir is required")
	}
	if configPath == "" {
		return nil, errors.New("server: NewDoltServer: configPath is required")
	}
	absDoltBinExec, err := filepath.Abs(doltBinExec)
	if err != nil {
		return nil, errors.New("server: NewDoltServer: failed to determine absolute path of doltBinExec")
	}
	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, errors.New("server: NewDoltServer: failed to determine absolute path of rootDir")
	}
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, errors.New("server: NewDoltServer: failed to determine absolute path of configPath")
	}
	cfg, err := servercfg.YamlConfigFromFile(filesys.LocalFS, configPath)
	if err != nil {
		return nil, fmt.Errorf("server: NewDoltServer: parse config %q: %w", configPath, err)
	}
	var logFile *os.File
	if logFilePath != "" {
		absLogFilePath, err := filepath.Abs(logFilePath)
		if err != nil {
			return nil, errors.New("server: NewDoltServer: failed to determine absolute path of logFilePath")
		}
		logFile, err = os.OpenFile(absLogFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // logFilePath is caller-derived, not user-request input
		if err != nil {
			return nil, fmt.Errorf("server: NewDoltServer: open log %q: %w", logFilePath, err)
		}
	}
	if keepAlivePeriod == 0 {
		keepAlivePeriod = defaultKeepAlivePeriod
	}
	sum := sha256.Sum256([]byte(rootDir))
	return &DoltServer{
		id:              hex.EncodeToString(sum[:]),
		doltBinExec:     absDoltBinExec,
		rootDir:         absRootDir,
		configPath:      absConfigPath,
		config:          cfg,
		keepAlivePeriod: keepAlivePeriod,
		logFile:         logFile,
	}, nil
}

func (s *DoltServer) ID(_ context.Context) string {
	return s.id
}

func (s *DoltServer) DSN(_ context.Context, database string) string {
	dsn := util.DoltServerDSN{
		User:        s.config.User(),
		Password:    s.config.Password(),
		Database:    database,
		TLSRequired: s.config.RequireSecureTransport(),
		TLSCert:     s.config.TLSCert(),
		TLSKey:      s.config.TLSKey(),
	}
	if sock := s.config.Socket(); sock != "" {
		dsn.Socket = sock
	} else {
		dsn.Host = s.config.Host()
		dsn.Port = s.config.Port()
	}
	return dsn.String()
}

func (s *DoltServer) Start(_ context.Context) error {
	args := []string{
		"sql-server",
		"-c", s.configPath,
	}

	if s.eg != nil || s.egCtx != nil {
		return fmt.Errorf("server: DoltServer.Start: server already started")
	}

	managedCtx, cancel := context.WithCancel(context.Background())
	eg, egCtx := errgroup.WithContext(managedCtx)
	s.eg = eg
	s.egCtx = egCtx
	s.cancel = cancel

	cmd := exec.CommandContext(managedCtx, s.doltBinExec, args...)
	cmd.Dir = s.rootDir
	cmd.Stdin = nil
	if s.logFile != nil {
		cmd.Stdout = s.logFile
		cmd.Stderr = s.logFile
	}

	cmd.Env = os.Environ()

	eg.Go(func() error {
		return cmd.Run()
	})

	// give server time to come up
	time.Sleep(200 * time.Millisecond)
	return nil
}

func (s *DoltServer) Stop(_ context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	var waitErr error
	if s.eg != nil {
		waitErr = s.eg.Wait()
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) || errors.Is(waitErr, context.Canceled) {
			waitErr = nil
		}
	}
	var closeErr error
	if s.logFile != nil {
		closeErr = s.logFile.Close()
		s.logFile = nil
	}
	if waitErr != nil {
		return fmt.Errorf("server: DoltServer.Stop: %w", waitErr)
	}
	if closeErr != nil {
		return fmt.Errorf("server: DoltServer.Stop: close log: %w", closeErr)
	}
	return nil
}

func (s *DoltServer) Restart(_ context.Context) error {
	return errors.New("server: DoltServer.Restart not implemented")
}

func (s *DoltServer) Running(_ context.Context) bool {
	return false
}

func (s *DoltServer) Ping(ctx context.Context) error {
	db, err := sql.Open("mysql", s.DSN(ctx, ""))
	if err != nil {
		return fmt.Errorf("server: DoltServer.Ping: open: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("server: DoltServer.Ping: %w", err)
	}
	return nil
}

func (s *DoltServer) Dial(ctx context.Context) (net.Conn, error) {
	network, addr := "tcp", net.JoinHostPort(s.config.Host(), strconv.Itoa(s.config.Port()))
	if sock := s.config.Socket(); sock != "" {
		network, addr = "unix", sock
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("server: DoltServer.Dial: %w", err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(s.keepAlivePeriod)
	}
	return conn, nil
}
