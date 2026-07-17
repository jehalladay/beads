package lockfile

import (
	"fmt"
	"testing"
)

// IsLocked must recognize BOTH lock-held sentinels. The shared-lock functions
// (FlockSharedNonBlock/FlockExclusiveNonBlock) return ErrLockBusy on contention,
// which — like ErrLocked — means "a lock is held by another process", the exact
// condition IsLocked documents. Before beads-43p8, IsLocked only matched
// errProcessLocked, so IsLocked(ErrLockBusy) was false and bare-IsLocked callers
// misclassified shared-lock contention (internal/linear/synclock carried a
// manual `|| err == ErrLockBusy` workaround).
func TestIsLocked_ErrLockBusy(t *testing.T) {
	if !IsLocked(ErrLockBusy) {
		t.Error("IsLocked(ErrLockBusy) = false, want true (ErrLockBusy means lock held by another process)")
	}
	// Wrapped ErrLockBusy must also match (errors.Is semantics).
	wrapped := fmt.Errorf("acquire shared lock: %w", ErrLockBusy)
	if !IsLocked(wrapped) {
		t.Errorf("IsLocked(wrapped ErrLockBusy) = false, want true, for: %v", wrapped)
	}
	// Regression guards: the existing contract stays intact.
	if !IsLocked(ErrLocked) {
		t.Error("IsLocked(ErrLocked) should stay true")
	}
	if IsLocked(nil) {
		t.Error("IsLocked(nil) should stay false")
	}
	if IsLocked(fmt.Errorf("unrelated")) {
		t.Error("IsLocked(unrelated) should stay false")
	}
}
