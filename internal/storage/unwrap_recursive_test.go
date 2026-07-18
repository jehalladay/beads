package storage

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-i1ff: UnwrapStore must transparently unwrap ANY decorator that exposes
// an Inner() DoltStorage accessor (not just *HookFiringStore), and must do so
// RECURSIVELY so stacked decorators (e.g. hook -> telemetry -> real) resolve to
// the concrete inner store. Otherwise a capability type-assert
// (UnwrapStore(store).(BackupStore) etc.) silently fails on a wrapped store and
// backup/compact/GC/restore/doctor become no-ops with no error.

// fakeUnwrapDecorator is a minimal decorator that is NOT *HookFiringStore but
// exposes Inner() DoltStorage — the exact shape a future telemetry/other
// decorator would take.
type fakeUnwrapDecorator struct {
	DoltStorage
	inner DoltStorage
}

func (d *fakeUnwrapDecorator) Inner() DoltStorage { return d.inner }

func TestUnwrapStore_UnwrapsGenericInnerDecorator(t *testing.T) {
	real := fakeHookStore{issues: map[string]*types.Issue{}}
	dec := &fakeUnwrapDecorator{inner: real}

	got := UnwrapStore(dec)
	if _, isDecorator := got.(*fakeUnwrapDecorator); isDecorator {
		t.Fatalf("UnwrapStore returned the decorator itself; want the inner store")
	}
	if _, isReal := got.(fakeHookStore); !isReal {
		t.Fatalf("UnwrapStore(dec) = %T, want fakeHookStore (the real inner store)", got)
	}
}

func TestUnwrapStore_UnwrapsRecursivelyThroughStackedDecorators(t *testing.T) {
	real := fakeHookStore{issues: map[string]*types.Issue{}}
	// Stack: generic decorator wrapping a HookFiringStore wrapping the real store.
	hook := NewHookFiringStore(real, nil)
	outer := &fakeUnwrapDecorator{inner: hook}

	got := UnwrapStore(outer)
	if _, isReal := got.(fakeHookStore); !isReal {
		t.Fatalf("UnwrapStore(outer) = %T, want fakeHookStore after recursive unwrap through hook+decorator", got)
	}
}

func TestUnwrapStore_ConcreteStoreUnchanged(t *testing.T) {
	real := fakeHookStore{issues: map[string]*types.Issue{}}
	got := UnwrapStore(real)
	if _, isReal := got.(fakeHookStore); !isReal {
		t.Fatalf("UnwrapStore(real) = %T, want the same concrete store unchanged", got)
	}
}
