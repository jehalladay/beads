package server

import (
	"context"
	"errors"
	"net"

	"github.com/dolthub/dolt/go/libraries/doltcore/servercfg"
)

type DoltServer struct {
	rootDir    string
	configPath string
	config     servercfg.ServerConfig
}

var _ DatabaseServer = (*DoltServer)(nil)

func NewDoltServer(rootDir, configPath string) (*DoltServer, error) {
	return nil, errors.New("server: NewDoltServer not implemented")
}

func (s *DoltServer) ID(_ context.Context) string {
	return ""
}

func (s *DoltServer) DSN(_ context.Context) string {
	return ""
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
