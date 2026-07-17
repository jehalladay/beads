package domain

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type DoltRemoteUseCase interface {
	CreateRemote(ctx context.Context, name, url string) error
	UpdateRemote(ctx context.Context, name, url string) error
	DeleteRemote(ctx context.Context, name string) error
	ListRemotes(ctx context.Context) ([]Remote, error)
}

type Remote struct {
	Name string
	URL  string
}

type RemoteSQLRepository interface {
	AddRemote(ctx context.Context, name, url string) error
	RemoveRemote(ctx context.Context, name string) error
	ListRemotes(ctx context.Context) ([]Remote, error)
}

func NewDoltRemoteUseCase(remoteRepo RemoteSQLRepository) DoltRemoteUseCase {
	return &doltRemoteUseCaseImpl{remoteRepo: remoteRepo}
}

type doltRemoteUseCaseImpl struct {
	remoteRepo RemoteSQLRepository
}

var _ DoltRemoteUseCase = (*doltRemoteUseCaseImpl)(nil)

func (u *doltRemoteUseCaseImpl) CreateRemote(ctx context.Context, name, url string) error {
	if name == "" {
		return fmt.Errorf("CreateRemote: name must not be empty")
	}
	if url == "" {
		return fmt.Errorf("CreateRemote: url must not be empty")
	}
	if err := u.remoteRepo.AddRemote(ctx, name, url); err != nil {
		return fmt.Errorf("CreateRemote %s: %w", name, err)
	}
	return nil
}

func (u *doltRemoteUseCaseImpl) UpdateRemote(ctx context.Context, name, url string) error {
	if name == "" {
		return fmt.Errorf("UpdateRemote: name must not be empty")
	}
	if url == "" {
		return fmt.Errorf("UpdateRemote: url must not be empty")
	}
	// Dolt has no atomic remote update, so this is remove-then-add. Capture
	// the old URL first so a failed add can restore the remote instead of
	// leaving it deleted (bd-6dnrw.44 P3).
	var oldURL string
	if remotes, err := u.remoteRepo.ListRemotes(ctx); err == nil {
		for _, rem := range remotes {
			if rem.Name == name {
				oldURL = rem.URL
				break
			}
		}
	}
	if err := u.remoteRepo.RemoveRemote(ctx, name); err != nil {
		return fmt.Errorf("UpdateRemote %s: remove: %w", name, err)
	}
	if err := u.remoteRepo.AddRemote(ctx, name, url); err != nil {
		if oldURL != "" {
			if restoreErr := u.remoteRepo.AddRemote(ctx, name, oldURL); restoreErr != nil {
				return fmt.Errorf("UpdateRemote %s: add: %w (restoring previous URL %s also failed: %v)", name, err, redactRemoteURL(oldURL), restoreErr)
			}
			return fmt.Errorf("UpdateRemote %s: add: %w (previous URL %s restored)", name, err, redactRemoteURL(oldURL))
		}
		return fmt.Errorf("UpdateRemote %s: add: %w", name, err)
	}
	return nil
}

func (u *doltRemoteUseCaseImpl) DeleteRemote(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("DeleteRemote: name must not be empty")
	}
	if err := u.remoteRepo.RemoveRemote(ctx, name); err != nil {
		return fmt.Errorf("DeleteRemote %s: %w", name, err)
	}
	return nil
}

func (u *doltRemoteUseCaseImpl) ListRemotes(ctx context.Context) ([]Remote, error) {
	remotes, err := u.remoteRepo.ListRemotes(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListRemotes: %w", err)
	}
	return remotes, nil
}

// redactRemoteURL strips credentials from a remote URL for safe inclusion in
// an error message. domain cannot import versioncontrolops.SanitizeURL
// (versioncontrolops imports domain — a cycle), so the logic is mirrored here:
// clear userinfo + query + fragment, redact wholesale when creds hide in an
// opaque/path component (url.Parse routes "user:pass@host" with no // authority
// to an opaque URL with a nil User), placeholder on parse failure so a
// malformed credential-bearing string is never echoed raw (beads-dsib).
func redactRemoteURL(raw string) string {
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<redacted-url>"
	}
	seg := parsed.Opaque
	if seg == "" {
		seg = parsed.Path
	}
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	if parsed.Host == "" && strings.Contains(seg, "@") {
		return "<redacted-url>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
