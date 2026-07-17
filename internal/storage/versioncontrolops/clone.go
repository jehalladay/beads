package versioncontrolops

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// DoltClone clones a Dolt database from a remote URL.
// conn must be a non-transactional database connection.
// The database parameter specifies the local database name for the clone.
func DoltClone(ctx context.Context, conn DBConn, remoteURL, database string) error {
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CLONE(?, ?)", remoteURL, database); err != nil {
		return fmt.Errorf("dolt clone %s: %w", SanitizeURL(remoteURL), err)
	}
	return nil
}

// SanitizeURL removes credentials from a URL so it is safe to print in errors
// and status messages. Callers that echo a user-supplied remote URL (which may
// embed user:token@host) MUST route it through here — both failure AND success
// paths (beads-enax): a leak on the happy path is just as bad as on the error path.
func SanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		// Never echo the raw string on parse failure — a malformed but
		// credential-bearing URL would leak user:pass into the error. Redact
		// instead (beads-cc1).
		return "<redacted-url>"
	}
	// url.Parse does not always route credentials through parsed.User. A
	// "user:pass@host/path" string with no // authority parses as an OPAQUE
	// URL (scheme="user", opaque="pass@host/..."), and a truly schemeless
	// "user:pass@host" leaves Host empty with the creds in Path/Opaque — in
	// both cases clearing parsed.User leaves the secret intact. When there is
	// no parsed Host (no proper // authority) but the raw string carries
	// "userinfo@authority" before the first '/', redact wholesale (beads-enax).
	if parsed.Host == "" && hasUserinfoBeforePath(raw) {
		return "<redacted-url>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// hasUserinfoBeforePath reports whether raw looks like "userinfo@authority..."
// with the '@' occurring before any '/' — i.e. embedded credentials that
// url.Parse did not lift into URL userinfo (opaque or schemeless forms).
func hasUserinfoBeforePath(raw string) bool {
	at := strings.IndexByte(raw, '@')
	if at < 0 {
		return false
	}
	slash := strings.IndexByte(raw, '/')
	return slash < 0 || at < slash
}
