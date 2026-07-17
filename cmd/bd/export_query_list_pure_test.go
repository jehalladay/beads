package main

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/query"
	"github.com/steveyegge/beads/internal/types"
)

// beads-52nf: hermetic tests for pure helpers across export.go, query.go, and
// list.go (all verified 0% + no test references).

func TestSanitizeZeroTime(t *testing.T) {
	epoch := time.Unix(0, 0).UTC()

	// Zero timestamps are replaced with the Unix epoch.
	iss := &types.Issue{}
	sanitizeZeroTime(iss)
	if !iss.CreatedAt.Equal(epoch) || !iss.UpdatedAt.Equal(epoch) {
		t.Errorf("zero times not set to epoch: created=%v updated=%v", iss.CreatedAt, iss.UpdatedAt)
	}

	// Non-zero timestamps are left untouched.
	real := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	iss2 := &types.Issue{CreatedAt: real, UpdatedAt: real}
	sanitizeZeroTime(iss2)
	if !iss2.CreatedAt.Equal(real) || !iss2.UpdatedAt.Equal(real) {
		t.Errorf("non-zero times were modified: %v %v", iss2.CreatedAt, iss2.UpdatedAt)
	}
}

func TestFilterOutPollutionFn(t *testing.T) {
	in := []*types.Issue{
		{Title: "real work item"},
		{Title: "test-something"}, // matches test prefix
		{Title: "benchmark_run"},  // matches benchmark prefix
		{Title: "legitimate task"},
		{Title: "tmp-scratch"}, // matches tmp prefix
	}
	out := filterOutPollution(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 clean issues, got %d: %+v", len(out), out)
	}
	for _, iss := range out {
		if strings.HasPrefix(iss.Title, "test") || strings.HasPrefix(iss.Title, "benchmark") || strings.HasPrefix(iss.Title, "tmp") {
			t.Errorf("pollution not filtered: %q", iss.Title)
		}
	}
	if len(filterOutPollution(nil)) != 0 {
		t.Error("nil → empty")
	}
}

func TestHasExplicitStatusFilter(t *testing.T) {
	cases := map[string]bool{
		"status=open":                           true,
		"priority=1":                            false,
		"status=open AND priority=1":            true,
		"priority=1 AND type=bug":               false,
		"status=open OR type=bug":               true,
		"NOT status=closed":                     true,
		"NOT priority=1":                        false,
		"(type=bug OR label=x) AND status=open": true,
	}
	for q, want := range cases {
		node, err := query.Parse(q)
		if err != nil {
			t.Fatalf("parse %q: %v", q, err)
		}
		if got := hasExplicitStatusFilter(node); got != want {
			t.Errorf("hasExplicitStatusFilter(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestSkipLabelsFooterText(t *testing.T) {
	got := skipLabelsFooterText()
	if !strings.Contains(got, "--skip-labels") || !strings.Contains(got, "suppressed") {
		t.Errorf("footer text unexpected: %q", got)
	}
}

func TestIssueOrNil(t *testing.T) {
	if issueOrNil(nil) != nil {
		t.Error("nil IssueWithCounts → nil")
	}
	iss := &types.Issue{ID: "bd-1"}
	if got := issueOrNil(&types.IssueWithCounts{Issue: iss}); got != iss {
		t.Errorf("issueOrNil should return the embedded issue, got %v", got)
	}
}

func TestPrintSkipLabelsFooter(t *testing.T) {
	// When skipLabels is false, nothing is printed regardless of quiet state.
	out := captureStdout(t, func() error {
		printSkipLabelsFooter(false)
		return nil
	})
	if out != "" {
		t.Errorf("skipLabels=false should print nothing, got %q", out)
	}

	// When skipLabels is true, output is gated by isQuiet() (global state we do
	// not control here): it must be either the footer or empty, never anything
	// else — and must not panic.
	out = captureStdout(t, func() error {
		printSkipLabelsFooter(true)
		return nil
	})
	if out != "" && !strings.Contains(out, "--skip-labels") {
		t.Errorf("skipLabels=true output should be the footer or empty, got %q", out)
	}
}
