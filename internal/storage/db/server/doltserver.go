package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"

	"github.com/dolthub/dolt/go/libraries/doltcore/servercfg"
	"github.com/dolthub/dolt/go/libraries/utils/filesys"

	"github.com/steveyegge/beads/internal/storage/doltutil"
)

type DoltServer struct {
	id         string
	rootDir    string
	configPath string
	config     servercfg.ServerConfig
}

var _ DatabaseServer = (*DoltServer)(nil)

func NewDoltServer(rootDir, configPath string) (*DoltServer, error) {
	if rootDir == "" {
		return nil, errors.New("server: NewDoltServer: rootDir is required")
	}
	if configPath == "" {
		return nil, errors.New("server: NewDoltServer: configPath is required")
	}
	cfg, err := servercfg.YamlConfigFromFile(filesys.LocalFS, configPath)
	if err != nil {
		return nil, fmt.Errorf("server: NewDoltServer: parse config %q: %w", configPath, err)
	}
	sum := sha256.Sum256([]byte(rootDir))
	return &DoltServer{
		id:         hex.EncodeToString(sum[:]),
		rootDir:    rootDir,
		configPath: configPath,
		config:     cfg,
	}, nil
}

func (s *DoltServer) ID(_ context.Context) string {
	return s.id
}

// TODO: support unix-socket connections (servercfg.Socket()) instead of
// always building a TCP DSN.
func (s *DoltServer) DSN(_ context.Context, database string) string {
	return doltutil.ServerDSN{
		Host:     s.config.Host(),
		Port:     s.config.Port(),
		User:     s.config.User(),
		Password: s.config.Password(),
		Database: database,
		TLS:      false,
	}.String()
}

func (s *DoltServer) Start(_ context.Context) error {
	return errors.New("server: DoltServer.Start not implemented")
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
