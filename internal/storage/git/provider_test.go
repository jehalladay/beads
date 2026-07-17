package git

import "testing"

func TestNewGitProvider_ReturnsUseCase(t *testing.T) {
	p := NewGitProvider("/tmp/workdir")
	if p == nil {
		t.Fatal("NewGitProvider returned nil")
	}
	if uc := p.GitUseCase(); uc == nil {
		t.Fatal("GitUseCase() returned nil")
	}
}

func TestGitUseCase_Memoized(t *testing.T) {
	p := NewGitProvider("/tmp/workdir")

	// The accessor lazily builds the use case once and caches it; repeated
	// calls must return the identical instance, not rebuild each time.
	first := p.GitUseCase()
	second := p.GitUseCase()
	if first != second {
		t.Error("GitUseCase() is not memoized: two calls returned different instances")
	}
}
