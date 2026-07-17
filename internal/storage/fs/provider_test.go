package fs

import (
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain"
)

func TestNewFileSystemProvider_ReturnsUseCase(t *testing.T) {
	p := NewFileSystemProvider("/tmp/workdir", domain.BeadsDirTemplates{}, domain.BeadsDirFSAdapters{})
	if p == nil {
		t.Fatal("NewFileSystemProvider returned nil")
	}
	if uc := p.BeadsDirFSUseCase(); uc == nil {
		t.Fatal("BeadsDirFSUseCase() returned nil")
	}
}

func TestBeadsDirFSUseCase_Memoized(t *testing.T) {
	p := NewFileSystemProvider("/tmp/workdir", domain.BeadsDirTemplates{}, domain.BeadsDirFSAdapters{})

	// The accessor lazily builds the use case once and caches it; repeated
	// calls must return the identical instance, not rebuild each time.
	first := p.BeadsDirFSUseCase()
	second := p.BeadsDirFSUseCase()
	if first != second {
		t.Error("BeadsDirFSUseCase() is not memoized: two calls returned different instances")
	}
}
