package server

import (
	"context"
	"net"
)

// DatabaseServer is the contract a backend must satisfy to sit behind the
// proxy. Every method takes a context so callers can bound or cancel any
// operation — implementations MUST honor cancellation, especially in Start /
// Stop / Dial. The proxy relies on this to avoid wedging on a misbehaving
// backend during shutdown.
type DatabaseServer interface {
	ID(ctx context.Context) string
	// DSN returns a connection string to the backend. If database is
	// non-empty, the resulting DSN selects that database; otherwise the
	// caller must select one explicitly (e.g. via USE). user and password
	// are the credentials embedded in the returned DSN — implementations
	// must use exactly what the caller passed, not any internally stored
	// credentials.
	DSN(ctx context.Context, database, user, password string) string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Running(ctx context.Context) bool
	Ping(ctx context.Context) error
	Dial(ctx context.Context) (net.Conn, error)
}
