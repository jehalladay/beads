package doltserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/storage/db/proxy"
	"github.com/steveyegge/beads/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDoltServerStore_ValidationErrors(t *testing.T) {
	cases := []struct {
		name     string
		database string
		rootUser string
		doltBin  string
		backend  proxy.Backend
		want     string
	}{
		{"empty database", "", "root", "/usr/bin/true", proxy.BackendLocalServer, "database name must not be empty"},
		{"invalid backend", "beads", "root", "/usr/bin/true", proxy.Backend("nope"), "unknown backend"},
		{"empty rootUser", "beads", "", "/usr/bin/true", proxy.BackendLocalServer, "rootUser must not be empty"},
		{"empty doltBin", "beads", "root", "", proxy.BackendLocalServer, "doltBinExec must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := newDoltServerStore(
				context.Background(),
				t.TempDir(), t.TempDir(),
				tc.database, "Test", "test@example.com",
				"", "", tc.backend, false,
				tc.rootUser, "", tc.doltBin,
			)
			assert.Nil(t, s)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestNewDoltServerStore_HappyPath(t *testing.T) {
	testutil.RequireDoltBinary(t)
	bin, err := exec.LookPath("dolt")
	require.NoError(t, err)

	t.Setenv("HOME", t.TempDir())

	storeRootDir := t.TempDir()

	store, err := newDoltServerStore(
		context.Background(),
		storeRootDir,
		t.TempDir(),
		"beads",
		"test_user",
		"test@example.com",
		"",
		"",
		proxy.BackendLocalServer,
		false,
		"root",
		"",
		bin,
	)

	require.NoError(t, err)
	require.NotNil(t, store)
	t.Cleanup(func() { _ = store.db.Close() })
}
