package main

import "testing"

// beads-i0wm: hermetic tests for the pure drift helpers in config_apply.go
// (verified 0% + no test refs).

func TestDriftDomain(t *testing.T) {
	cases := map[string]string{
		"dolt.host":         "dolt",         // splits on the first dot
		"remote.origin.url": "remote",       // only the first segment
		"issue_prefix":      "issue_prefix", // no dot → returned whole
		"":                  "",
	}
	for in, want := range cases {
		if got := driftDomain(in); got != want {
			t.Errorf("driftDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSkippedDriftItemAndDomain(t *testing.T) {
	items := []DriftItem{
		{Check: "dolt.host", Status: "drift"},
		{Check: "remote.origin.url", Status: driftStatusSkipped, Message: "remote skipped"},
		{Check: "backup.enabled", Status: "ok"},
	}

	t.Run("skippedDriftItem returns the matching skipped item", func(t *testing.T) {
		got := skippedDriftItem(items, "remote")
		if got == nil || got.Check != "remote.origin.url" || got.Message != "remote skipped" {
			t.Fatalf("expected the skipped remote item, got %+v", got)
		}
	})

	t.Run("skippedDriftItem returns nil for a non-skipped domain", func(t *testing.T) {
		// dolt.host is present but its status is "drift", not skipped.
		if got := skippedDriftItem(items, "dolt"); got != nil {
			t.Errorf("expected nil for a non-skipped domain, got %+v", got)
		}
	})

	t.Run("skippedDriftItem returns nil for an absent domain", func(t *testing.T) {
		if got := skippedDriftItem(items, "nonexistent"); got != nil {
			t.Errorf("expected nil for an absent domain, got %+v", got)
		}
	})

	t.Run("skippedDriftDomain mirrors item presence", func(t *testing.T) {
		if !skippedDriftDomain(items, "remote") {
			t.Error("expected skippedDriftDomain(remote) = true")
		}
		if skippedDriftDomain(items, "dolt") {
			t.Error("expected skippedDriftDomain(dolt) = false (status is drift, not skipped)")
		}
		if skippedDriftDomain(nil, "remote") {
			t.Error("expected false for a nil item list")
		}
	})
}
