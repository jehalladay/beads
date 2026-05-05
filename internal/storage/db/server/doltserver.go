package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dolthub/dolt/go/libraries/doltcore/servercfg"
	"github.com/dolthub/dolt/go/libraries/utils/filesys"
	"golang.org/x/sync/errgroup"

	"github.com/steveyegge/beads/internal/storage/db/util"
)

type DoltServer struct {
	id          string
	doltBinExec string
	rootDir     string
	configPath  string
	config      servercfg.ServerConfig

	cmd     *exec.Cmd       // the dolt sql-server process; nil when not started
	logFile *os.File        // stdout/stderr destination; closed by Stop
	errChan chan error      // single-buffered; receives cmd.Wait() result on non-nil error
	egCtx   context.Context // canceled when the wait goroutine returns an error or parent ctx cancels
}

var _ DatabaseServer = (*DoltServer)(nil)

func NewDoltServer(doltBinExec, rootDir, configPath, logFilePath string) (*DoltServer, error) {
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
	sum := sha256.Sum256([]byte(rootDir))
	return &DoltServer{
		id:          hex.EncodeToString(sum[:]),
		doltBinExec: absDoltBinExec,
		rootDir:     absRootDir,
		configPath:  absConfigPath,
		config:      cfg,
		logFile:     logFile,
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

func (s *DoltServer) Start(ctx context.Context) error {
	args := []string{
		"sql-server",
		"-c", s.configPath,
	}

	cmd := exec.Command(s.doltBinExec, args...)
	cmd.Dir = s.rootDir
	cmd.Stdin = nil
	if s.logFile != nil {
		cmd.Stdout = s.logFile
		cmd.Stderr = s.logFile
	}
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("server: DoltServer.Start: launch dolt sql-server: %w", err)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	s.cmd = cmd
	s.errChan = make(chan error, 1)
	s.egCtx = egCtx

	eg.Go(func() error {
		defer close(s.errChan)
		waitErr := cmd.Wait()
		s.errChan <- waitErr
		return waitErr
	})

	return nil
}

func (s *DoltServer) Stop(_ context.Context) error {
	return errors.New("server: DoltServer.Stop not implemented")
}

func (s *DoltServer) Restart(_ context.Context) error {
	return errors.New("server: DoltServer.Restart not implemented")
}

func (s *DoltServer) Running(_ context.Context) bool {
	return false
}

func (s *DoltServer) Ping(_ context.Context) error {
	return errors.New("server: DoltServer.Ping not implemented")
}

func (s *DoltServer) Dial(_ context.Context) (net.Conn, error) {
	return nil, errors.New("server: DoltServer.Dial not implemented")
}
