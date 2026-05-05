package server

import (
	"context"
	"net"
)

type DatabaseServer interface {
	ID() string
	Start() error
	Stop() error
	Restart() error
	Running() bool
	Ping(ctx context.Context) error
	Dial(ctx context.Context) (net.Conn, error)
	DSN() string
}
