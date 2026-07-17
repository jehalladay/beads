// Package storage — iter_test.go
//
// Hermetic unit tests for the pure generic iterator API in iter.go
// (SliceIter[T], NewSliceIter, ForEach, Collect). No DB, no backend —
// SliceIter is the in-memory stub, so these exercise the lifecycle
// contract documented on the Iter interface without any I/O.
package storage

import (
	"context"
	"errors"
	"testing"
)

func TestSliceIter_Empty(t *testing.T) {
	it := NewSliceIter([]*int{})
	ctx := context.Background()

	// Value before any Next() must be nil (idx == -1).
	if got := it.Value(); got != nil {
		t.Fatalf("Value() before Next() = %v, want nil", got)
	}
	// Next on an empty slice reports no rows.
	if it.Next(ctx) {
		t.Fatal("Next() on empty iterator = true, want false")
	}
	// Value after exhaustion stays nil.
	if got := it.Value(); got != nil {
		t.Fatalf("Value() after exhaustion = %v, want nil", got)
	}
	// Err is always nil for SliceIter.
	if err := it.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if err := it.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

func TestSliceIter_NilSlice(t *testing.T) {
	it := NewSliceIter[int](nil)
	if it.Next(context.Background()) {
		t.Fatal("Next() on nil-backed iterator = true, want false")
	}
	if got := it.Value(); got != nil {
		t.Fatalf("Value() = %v, want nil", got)
	}
}

func TestSliceIter_SingleAndMulti(t *testing.T) {
	tests := []struct {
		name string
		vals []int
	}{
		{"single", []int{7}},
		{"multi", []int{1, 2, 3}},
	}
	ctx := context.Background()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]*int, len(tc.vals))
			for i := range tc.vals {
				v := tc.vals[i]
				items[i] = &v
			}
			it := NewSliceIter(items)

			var got []int
			for it.Next(ctx) {
				p := it.Value()
				if p == nil {
					t.Fatal("Value() returned nil inside iteration")
				}
				got = append(got, *p)
			}
			// One extra Next past the end stays false.
			if it.Next(ctx) {
				t.Fatal("Next() past end = true, want false")
			}
			if it.Err() != nil {
				t.Fatalf("Err() = %v, want nil", it.Err())
			}
			if len(got) != len(tc.vals) {
				t.Fatalf("iterated %v, want %v", got, tc.vals)
			}
			for i := range tc.vals {
				if got[i] != tc.vals[i] {
					t.Fatalf("iterated %v, want %v", got, tc.vals)
				}
			}
		})
	}
}

func TestSliceIter_CloseShortCircuitsNext(t *testing.T) {
	v1, v2 := 1, 2
	it := NewSliceIter([]*int{&v1, &v2})
	ctx := context.Background()

	// Advance to the first element, then close mid-iteration.
	if !it.Next(ctx) {
		t.Fatal("first Next() = false, want true")
	}
	if err := it.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	// After Close, Next must report no more rows even though items remain.
	if it.Next(ctx) {
		t.Fatal("Next() after Close() = true, want false")
	}
	// Close is idempotent.
	if err := it.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}

func TestForEach_AllValues(t *testing.T) {
	vals := []int{10, 20, 30}
	items := make([]*int, len(vals))
	for i := range vals {
		v := vals[i]
		items[i] = &v
	}
	it := NewSliceIter(items)

	sum := 0
	err := ForEach(context.Background(), it, func(v *int) error {
		sum += *v
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach() = %v, want nil", err)
	}
	if sum != 60 {
		t.Fatalf("sum = %d, want 60", sum)
	}
	// ForEach must have closed the iterator: a fresh Next returns false.
	if it.Next(context.Background()) {
		t.Fatal("Next() after ForEach() = true, want false (not closed)")
	}
}

func TestForEach_FnErrorPropagatesAndCloses(t *testing.T) {
	v1, v2, v3 := 1, 2, 3
	it := &closeTrackingIter{SliceIter: NewSliceIter([]*int{&v1, &v2, &v3})}
	sentinel := errors.New("stop here")

	seen := 0
	err := ForEach(context.Background(), it, func(v *int) error {
		seen++
		if *v == 2 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ForEach() = %v, want %v", err, sentinel)
	}
	if seen != 2 {
		t.Fatalf("fn called %d times, want 2 (short-circuit on error)", seen)
	}
	if !it.closedOnce {
		t.Fatal("ForEach() did not Close() the iterator on fn error")
	}
}

func TestForEach_CloseErrorSurfacesWhenNoFnError(t *testing.T) {
	v1 := 1
	closeErr := errors.New("close failed")
	it := &closeTrackingIter{
		SliceIter: NewSliceIter([]*int{&v1}),
		closeErr:  closeErr,
	}
	err := ForEach(context.Background(), it, func(*int) error { return nil })
	if !errors.Is(err, closeErr) {
		t.Fatalf("ForEach() = %v, want the Close error %v", err, closeErr)
	}
}

func TestForEach_FnErrorTakesPrecedenceOverCloseError(t *testing.T) {
	v1, v2 := 1, 2
	closeErr := errors.New("close failed")
	fnErr := errors.New("fn failed")
	it := &closeTrackingIter{
		SliceIter: NewSliceIter([]*int{&v1, &v2}),
		closeErr:  closeErr,
	}
	err := ForEach(context.Background(), it, func(*int) error { return fnErr })
	if !errors.Is(err, fnErr) {
		t.Fatalf("ForEach() = %v, want fn error %v (fn err wins over close err)", err, fnErr)
	}
}

func TestCollect_RoundTrip(t *testing.T) {
	vals := []int{5, 6, 7}
	items := make([]*int, len(vals))
	for i := range vals {
		v := vals[i]
		items[i] = &v
	}
	out, err := Collect(context.Background(), NewSliceIter(items))
	if err != nil {
		t.Fatalf("Collect() = %v, want nil", err)
	}
	if len(out) != len(vals) {
		t.Fatalf("Collect() len = %d, want %d", len(out), len(vals))
	}
	for i := range vals {
		if *out[i] != vals[i] {
			t.Fatalf("Collect()[%d] = %d, want %d", i, *out[i], vals[i])
		}
	}
}

func TestCollect_Empty(t *testing.T) {
	out, err := Collect(context.Background(), NewSliceIter([]*int{}))
	if err != nil {
		t.Fatalf("Collect() = %v, want nil", err)
	}
	if len(out) != 0 {
		t.Fatalf("Collect() = %v, want empty", out)
	}
}

func TestCollect_PropagatesIterError(t *testing.T) {
	iterErr := errors.New("iteration blew up")
	it := &erroringIter{err: iterErr}
	out, err := Collect(context.Background(), it)
	if !errors.Is(err, iterErr) {
		t.Fatalf("Collect() err = %v, want %v", err, iterErr)
	}
	if out != nil {
		t.Fatalf("Collect() out = %v, want nil on error", out)
	}
}

// closeTrackingIter wraps a SliceIter to observe Close() calls and inject a
// Close error, so ForEach's always-close + error-precedence contract can be
// asserted.
type closeTrackingIter struct {
	*SliceIter[int]
	closeErr   error
	closedOnce bool
}

func (it *closeTrackingIter) Close() error {
	it.closedOnce = true
	_ = it.SliceIter.Close()
	return it.closeErr
}

// erroringIter yields no rows and reports an error from Err(), modeling a
// backend iterator that fails during setup/iteration.
type erroringIter struct {
	err    error
	closed bool
}

func (it *erroringIter) Next(context.Context) bool { return false }
func (it *erroringIter) Value() *int               { return nil }
func (it *erroringIter) Err() error                { return it.err }
func (it *erroringIter) Close() error {
	it.closed = true
	return nil
}
