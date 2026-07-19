package doltserver

import (
	"os"
	"testing"
)

// TestMain arms BEADS_TEST_MODE=1 for the whole doltserver test package
// (hq-sl5wbc). This package has the most real dolt sql-server starts in the
// suite and previously had NO TestMain, so it was the one package where a test
// going through the dolt config path could inherit the ambient crew shell's
// BEADS_DOLT_SERVER_HOST/PORT (the production hub) with no guard armed. With
// BEADS_TEST_MODE=1 set, the store guard (internal/storage/dolt/store.go:
// applyConfigDefaults + New) neutralizes a prod host→127.0.0.1 and the prod
// port→1, so no doltserver test can ever reach the production hub.
//
// The package's own servers are unaffected: they bind 127.0.0.1 + ephemeral
// ports (loopback + non-3307), which the guard explicitly leaves intact.
func TestMain(m *testing.M) {
	prev, had := os.LookupEnv("BEADS_TEST_MODE")
	_ = os.Setenv("BEADS_TEST_MODE", "1")
	code := m.Run()
	if had {
		_ = os.Setenv("BEADS_TEST_MODE", prev)
	} else {
		_ = os.Unsetenv("BEADS_TEST_MODE")
	}
	os.Exit(code)
}
