package server

import (
	"context"
	"net"
)

// DatabaseServer is the backend the proxy fronts. Implementations decide what
// "the backend" is: a child process on the same host, a managed remote
// instance reachable over the network, or an always-on external endpoint.
//
// Lifecycle methods (Start/Stop/Restart/Running/Ping) reflect that choice. For
// a local-process impl, Start spawns; for a managed remote, Start might call
// a cloud API; for an always-on remote, Start is a no-op and Running/Ping
// reflect reachability.
//
// Dial returns a fresh connection to the backend. The proxy calls it once per
// inbound client connection and io.Copy's bytes between the two. Implementations
// own the connection details — host, port, TLS config, dial timeout, custom
// dialers — so this single method covers local TCP, remote TCP, and remote
// TLS uniformly.
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
