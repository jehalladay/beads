package server

import (
	"errors"
	"fmt"
	"testing"
)

// isRetryableDoltInitErr classifies a dolt-init failure as transient (retry) or
// permanent by substring-matching the error text against a known-transient list.
func TestIsRetryableDoltInitErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not retryable", nil, false},
		{
			"exact transient substring",
			errors.New("repository state is invalid"),
			true,
		},
		{
			"transient substring embedded in a larger message",
			fmt.Errorf("dolt init: %w", errors.New("fatal: repository state is invalid; aborting")),
			true,
		},
		{
			"unrelated error is permanent",
			errors.New("permission denied"),
			false,
		},
		{
			"empty message is permanent",
			errors.New(""),
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableDoltInitErr(tt.err); got != tt.want {
				t.Errorf("isRetryableDoltInitErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
