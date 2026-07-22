package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
)

// beads-ufnyb: findMailDelegate() read the mail.delegate config only under
// `if store != nil`. In proxiedServerMode the global store is nil (main.go
// PersistentPreRun wires uowProvider and returns before store init), so a
// hub-connected crew that ran `bd config set mail.delegate "gt mail"` (which
// PERSISTS via runConfigSetProxiedServer) had that config silently ignored
// here — `bd mail` failed "no mail delegate configured" while direct crew with
// identical config worked. The proxied twin of TestMailDelegateFromConfig.
//
// The fix routes the config read through a fail-soft UOW when
// usesProxiedServer() && store == nil, mirroring config_proxied_server.go but
// swallowing errors to "" (findMailDelegate's documented empty-means-none
// contract must hold).
//
// MUTATION-VERIFY: delete the `else if usesProxiedServer() ...` branch in
// findMailDelegate → resolves_from_proxied_config FAILS (delegate resolves to
// "" because store is nil), while the env-var and fail-soft subtests stay green.

var errUfnybConfigRead = errors.New("injected config-read failure (ufnyb test)")

// fakeUfnybProvider hands out a fake UOW (or an open error) from NewUOW.
type fakeUfnybProvider struct {
	uw     *fakeUfnybUOW
	openErr error
}

func (p *fakeUfnybProvider) NewUOW(context.Context) (uow.UnitOfWork, error) {
	if p.openErr != nil {
		return nil, p.openErr
	}
	return p.uw, nil
}
func (p *fakeUfnybProvider) Close(context.Context) error { return nil }

// fakeUfnybUOW embeds uow.UnitOfWork (unused methods panic if hit) and overrides
// only ConfigUseCase()/Close, which is all findMailDelegate exercises.
type fakeUfnybUOW struct {
	uow.UnitOfWork
	config     *fakeUfnybConfigUC
	closeCalls int
}

func (u *fakeUfnybUOW) ConfigUseCase() domain.ConfigUseCase { return u.config }
func (u *fakeUfnybUOW) Close(context.Context)               { u.closeCalls++ }

// fakeUfnybConfigUC returns a configured value or an error for GetConfig.
type fakeUfnybConfigUC struct {
	domain.ConfigUseCase
	value string
	err   error
}

func (u *fakeUfnybConfigUC) GetConfig(context.Context, string) (string, error) {
	return u.value, u.err
}

func TestMailDelegateFromProxiedConfig_ufnyb(t *testing.T) {
	// Save/restore all globals findMailDelegate touches.
	origStore := store
	origProvider := uowProvider
	origProxied := proxiedServerMode
	origCtx := rootCtx
	origBeads := os.Getenv("BEADS_MAIL_DELEGATE")
	origBD := os.Getenv("BD_MAIL_DELEGATE")
	t.Cleanup(func() {
		store = origStore
		uowProvider = origProvider
		proxiedServerMode = origProxied
		rootCtx = origCtx
		os.Setenv("BEADS_MAIL_DELEGATE", origBeads)
		os.Setenv("BD_MAIL_DELEGATE", origBD)
	})

	// Proxied mode: global store is nil, uowProvider is wired.
	store = nil
	proxiedServerMode = true
	rootCtx = context.Background()
	os.Unsetenv("BEADS_MAIL_DELEGATE")
	os.Unsetenv("BD_MAIL_DELEGATE")

	t.Run("resolves_from_proxied_config", func(t *testing.T) {
		uw := &fakeUfnybUOW{config: &fakeUfnybConfigUC{value: "gt mail"}}
		uowProvider = &fakeUfnybProvider{uw: uw}

		got := findMailDelegate()
		if got != "gt mail" {
			t.Fatalf("findMailDelegate() = %q, want %q (proxied config read)", got, "gt mail")
		}
		if uw.closeCalls != 1 {
			t.Errorf("expected the read UOW to be closed exactly once, got %d", uw.closeCalls)
		}
	})

	t.Run("env_var_still_takes_priority", func(t *testing.T) {
		os.Setenv("BEADS_MAIL_DELEGATE", "env mail")
		defer os.Unsetenv("BEADS_MAIL_DELEGATE")
		// Config would return a different value; the env var must win and the
		// proxied config path must not even be consulted.
		uw := &fakeUfnybUOW{config: &fakeUfnybConfigUC{value: "gt mail"}}
		uowProvider = &fakeUfnybProvider{uw: uw}

		got := findMailDelegate()
		if got != "env mail" {
			t.Fatalf("findMailDelegate() = %q, want %q (env var priority)", got, "env mail")
		}
		if uw.closeCalls != 0 {
			t.Errorf("proxied config path should not run when an env var is set; UOW closed %d times", uw.closeCalls)
		}
	})

	t.Run("failsoft_on_open_error", func(t *testing.T) {
		uowProvider = &fakeUfnybProvider{openErr: errUfnybConfigRead}
		if got := findMailDelegate(); got != "" {
			t.Fatalf("findMailDelegate() = %q, want empty (fail-soft on UOW open error)", got)
		}
	})

	t.Run("failsoft_on_config_error", func(t *testing.T) {
		uw := &fakeUfnybUOW{config: &fakeUfnybConfigUC{err: errUfnybConfigRead}}
		uowProvider = &fakeUfnybProvider{uw: uw}
		if got := findMailDelegate(); got != "" {
			t.Fatalf("findMailDelegate() = %q, want empty (fail-soft on config error)", got)
		}
		if uw.closeCalls != 1 {
			t.Errorf("UOW should still be closed on a config error, got %d closes", uw.closeCalls)
		}
	})

	t.Run("empty_config_returns_empty", func(t *testing.T) {
		uw := &fakeUfnybUOW{config: &fakeUfnybConfigUC{value: ""}}
		uowProvider = &fakeUfnybProvider{uw: uw}
		if got := findMailDelegate(); got != "" {
			t.Fatalf("findMailDelegate() = %q, want empty (no delegate configured)", got)
		}
	})
}
