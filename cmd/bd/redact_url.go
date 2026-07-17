package main

import (
	"net/url"
	"strings"
)

// redactURLCredentials removes an embedded password/token from a URL while
// preserving the scheme, any bare username, host and path, so the result stays
// usable in a printed command hint. Git normally sources credentials from a
// credential helper rather than the URL, so stripping the secret keeps a
// "bd dolt remote add origin <url>" hint runnable.
//
// It is used for the several cmd/bd sites that print a git origin URL in
// non-error flows (repair hints, init suggestions, access messages). Those URLs
// come from `git remote get-url origin` and can embed user:token@host, which
// would otherwise leak into stderr/stdout/scrollback/CI (beads-v7zc). This is
// the same class as beads-enax (clone success prints) and beads-sh85
// (storage-layer bootstrap print).
//
// Only an actual secret is removed:
//   - "https://user:token@host/p" -> "https://user@host/p" (password stripped,
//     username kept — a bare username is not a secret and some flows need it).
//   - "ssh://git@host/p" / "git+ssh://git@host/p" -> unchanged (git@ is an SSH
//     username, no inline secret).
//   - scp-like "git@host:org/repo.git" -> unchanged (SSH, key-based; url.Parse
//     rejects it, but it carries no inline password).
//
// url.Parse routes "user:pass@host/path" (no // authority) to an OPAQUE URL
// (scheme=user, opaque=pass@host/...) with a nil User, so it must be redacted
// wholesale — the secret is not reachable via parsed.User there.
func redactURLCredentials(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		// scp-like "git@host:path" fails to parse but carries no inline
		// password; only redact wholesale if a ":" password separator appears
		// before the "@" (the user:pass@ shape).
		if hasInlinePasswordBeforeAt(raw) {
			return "<redacted-url>"
		}
		return raw
	}
	// Opaque/schemeless form: scheme=user, opaque="pass@host/...", nil User.
	if parsed.User == nil && parsed.Host == "" &&
		strings.Contains(firstURLSegment(parsed.Opaque, parsed.Path), "@") {
		return "<redacted-url>"
	}
	if parsed.User == nil {
		return raw // no userinfo at all
	}
	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return raw // bare username (e.g. ssh git@host) — not a secret
	}
	// Strip the password, keep the username so the hint stays runnable.
	parsed.User = url.User(parsed.User.Username())
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// hasInlinePasswordBeforeAt reports whether raw has a "user:pass@" shape, i.e.
// a ':' appears before the first '@' (and before any '/'), indicating an inline
// password. Used for URLs that url.Parse rejects.
func hasInlinePasswordBeforeAt(raw string) bool {
	at := strings.IndexByte(raw, '@')
	if at < 0 {
		return false
	}
	userinfo := raw[:at]
	if slash := strings.IndexByte(userinfo, '/'); slash >= 0 {
		return false // '@' is in the path, not userinfo
	}
	return strings.Contains(userinfo, ":")
}

// firstURLSegment returns the first non-empty candidate up to (but excluding)
// the first '/', used to detect a "userinfo@host" prefix on opaque/schemeless
// URLs.
func firstURLSegment(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if i := strings.IndexByte(c, '/'); i >= 0 {
			return c[:i]
		}
		return c
	}
	return ""
}
