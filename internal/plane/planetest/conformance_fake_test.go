package planetest

import (
	"testing"
)

// TestFakePlaneConformance runs the conformance suite against the in-process
// fake. This is what makes the fake trustworthy as a test double: it must
// pass the same battery a live Plane CE v1.3.0 instance passes.
func TestFakePlaneConformance(t *testing.T) {
	srv := NewServer(ServerConfig{
		APIKey:    "plane_api_fake_key",
		Workspace: "acme",
	})
	t.Cleanup(srv.Close)

	projectID := srv.AddProject("Gas City", "GC")

	RunConformance(t, ConformanceTarget{
		BaseURL:   srv.URL(),
		APIKey:    "plane_api_fake_key",
		Workspace: "acme",
		ProjectID: projectID,
		Live:      false,
	})
}
