package main

import "testing"

// TestRefuseMetadatalessRemoteServer covers the beads-h19n guard: bd must refuse
// to open a store against a non-local shared Dolt server when the workspace has
// no metadata.json, rather than silently reading/writing the production hub
// under the default database name.
func TestRefuseMetadatalessRemoteServer(t *testing.T) {
	// Isolate from any ambient shared-server env the test runner may carry.
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")

	cases := []struct {
		name       string
		cfgIsNil   bool
		serverMode bool
		envHost    string
		wantRefuse bool
		wantHost   string
	}{
		{
			name:       "no metadata + server mode + non-local host -> refuse",
			cfgIsNil:   true,
			serverMode: true,
			envHost:    "172.31.26.56",
			wantRefuse: true,
			wantHost:   "172.31.26.56",
		},
		{
			name:       "no metadata + server mode + localhost -> allow",
			cfgIsNil:   true,
			serverMode: true,
			envHost:    "127.0.0.1",
			wantRefuse: false,
			wantHost:   "127.0.0.1",
		},
		{
			name:       "no metadata + server mode + 0.0.0.0 (local) -> allow",
			cfgIsNil:   true,
			serverMode: true,
			envHost:    "0.0.0.0",
			wantRefuse: false,
		},
		{
			name:       "metadata present (cfg != nil) -> never refuse even with non-local host",
			cfgIsNil:   false,
			serverMode: true,
			envHost:    "172.31.26.56",
			wantRefuse: false,
		},
		{
			name:       "not server mode -> never refuse",
			cfgIsNil:   true,
			serverMode: false,
			envHost:    "172.31.26.56",
			wantRefuse: false,
		},
		{
			name:       "no metadata + server mode + no host set (defaults local) -> allow",
			cfgIsNil:   true,
			serverMode: true,
			envHost:    "",
			wantRefuse: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_SERVER_HOST", tc.envHost)
			host, refuse := refuseMetadatalessRemoteServer(tc.cfgIsNil, tc.serverMode)
			if refuse != tc.wantRefuse {
				t.Fatalf("refuseMetadatalessRemoteServer(cfgIsNil=%v, serverMode=%v) refuse=%v, want %v (host=%q)",
					tc.cfgIsNil, tc.serverMode, refuse, tc.wantRefuse, host)
			}
			if tc.wantRefuse && host != tc.wantHost {
				t.Fatalf("refused with host=%q, want %q", host, tc.wantHost)
			}
		})
	}
}
