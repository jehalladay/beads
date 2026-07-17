package configfile

import "testing"

// An out-of-range port in the env must be IGNORED (fall through to the next
// source) exactly like a non-numeric value — not returned verbatim. Before
// beads-114t, BEADS_DOLT_SERVER_PORT=-5/0/99999 were returned as-is, and =0 was
// worst: it returned 0 and skipped the valid struct fallback, silently dialing
// port 0. The [1,65535] bound mirrors ExternalDoltConfig.Validate.
func TestGetDoltServerPort_OutOfRangeEnvFallsThrough(t *testing.T) {
	isolateDoltServerEnv(t)
	for _, bad := range []string{"-5", "0", "65536", "99999", "70000"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_SERVER_PORT", bad)
			c := &Config{DoltServerPort: 3307}
			if got := c.GetDoltServerPort(); got != 3307 {
				t.Errorf("GetDoltServerPort() with bad env %q = %d, want fallthrough to struct 3307", bad, got)
			}
		})
	}
}

// The secondary env var (BEADS_DOLT_PORT) is range-checked the same way.
func TestGetDoltServerPort_SecondaryEnvOutOfRangeFallsThrough(t *testing.T) {
	isolateDoltServerEnv(t)
	t.Setenv("BEADS_DOLT_PORT", "0")
	c := &Config{DoltServerPort: 3307}
	if got := c.GetDoltServerPort(); got != 3307 {
		t.Errorf("GetDoltServerPort() with BEADS_DOLT_PORT=0 = %d, want fallthrough to 3307", got)
	}
}

// A valid in-range env port still wins (regression guard).
func TestGetDoltServerPort_ValidEnvStillWins(t *testing.T) {
	isolateDoltServerEnv(t)
	t.Setenv("BEADS_DOLT_SERVER_PORT", "9999")
	c := &Config{DoltServerPort: 3307}
	if got := c.GetDoltServerPort(); got != 9999 {
		t.Errorf("GetDoltServerPort() = %d, want 9999", got)
	}
	// Boundary values are valid.
	for _, ok := range []string{"1", "65535"} {
		t.Setenv("BEADS_DOLT_SERVER_PORT", ok)
		if got := c.GetDoltServerPort(); got < 1 || got > 65535 {
			t.Errorf("GetDoltServerPort() rejected valid boundary %q = %d", ok, got)
		}
	}
}

// The remotesapi port getter shares the same range guard.
func TestGetDoltRemotesAPIPort_OutOfRangeEnvFallsThrough(t *testing.T) {
	for _, bad := range []string{"-1", "0", "70000"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", bad)
			c := &Config{DoltRemotesAPIPort: 7000}
			if got := c.GetDoltRemotesAPIPort(); got != 7000 {
				t.Errorf("GetDoltRemotesAPIPort() with bad env %q = %d, want fallthrough to 7000", bad, got)
			}
		})
	}
}
