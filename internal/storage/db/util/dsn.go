package util

import (
	"fmt"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// DoltServerDSN holds connection parameters for building a MySQL DSN to a
// Dolt server fronted by the proxy. All DSNs built with this struct set
// parseTime=true and multiStatements=true.
type DoltServerDSN struct {
	Socket   string // Unix domain socket path; when set, Net="unix" and Host/Port are ignored
	Host     string
	Port     int
	User     string
	Password string //nolint:gosec // G117: MySQL DSN password field; required by the connection-string builder, not serialized as JSON
	Database string        // optional; empty connects without selecting a database
	Timeout  time.Duration // connect timeout; 0 defaults to 5s
	// TLSRequired is true when the server rejects non-TLS connections
	// (servercfg.RequireSecureTransport). When true, String() emits tls=true.
	TLSRequired bool
	// TLSCert and TLSKey are paths to PEM-encoded TLS material associated with
	// the server. They are carried on the DSN so consumers can wire them up;
	// String() does not yet register a custom mysql TLS config from them.
	// TODO: when set together with TLSRequired, register a custom config via
	// mysql.RegisterTLSConfig and emit tls=<name>.
	TLSCert string
	TLSKey  string
}

// String builds the MySQL DSN string. Always sets parseTime=true,
// multiStatements=true, allowNativePasswords=true, and a connect timeout.
func (d DoltServerDSN) String() string {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	net := "tcp"
	addr := fmt.Sprintf("%s:%d", d.Host, d.Port)
	if d.Socket != "" {
		net = "unix"
		addr = d.Socket
	}

	cfg := mysql.Config{
		User:                 d.User,
		Passwd:               d.Password,
		Net:                  net,
		Addr:                 addr,
		DBName:               d.Database,
		ParseTime:            true,
		MultiStatements:      true,
		Timeout:              timeout,
		AllowNativePasswords: true,
	}
	if d.TLSRequired {
		cfg.TLSConfig = "true"
	} else {
		// go-sql-driver/mysql v1.8+ defaults to tls=preferred when TLSConfig
		// is empty. Dolt servers without TLS reject preferred-mode negotiation
		// with "TLS requested but server does not support TLS". Explicitly
		// disable TLS so connections work against non-TLS Dolt instances.
		cfg.TLSConfig = "false"
	}

	return cfg.FormatDSN()
}
