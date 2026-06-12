// Package planetest provides an in-process fake Plane CE server and a
// conformance suite that runs identically against the fake and a live Plane
// instance. The fake is only trusted because it passes the same suite a
// real v1.3.0 deployment passes — when the suite grows, the fake must keep
// up, and when Plane's behavior changes, the live run catches the drift.
package planetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/plane"
)

// ConformanceTarget describes the Plane instance under test.
type ConformanceTarget struct {
	// BaseURL is the instance root, e.g. https://plane.example.com or an
	// httptest server URL.
	BaseURL string
	// APIKey authenticates every request.
	APIKey string
	// Workspace is the workspace slug.
	Workspace string
	// ProjectID is the UUID of a project the suite may freely write to.
	// Live runs should point this at a dedicated throwaway project.
	ProjectID string
	// Live is true when running against a real deployment. Behaviors that
	// cannot be exercised safely or deterministically against a live
	// instance (auth-failure bodies, rate-limit injection) only run when
	// Live is false.
	Live bool
}

// uniqueSuffix returns a suffix for entity names so that live conformance
// runs do not collide with leftovers from previous runs.
func uniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// RunConformance executes the full conformance battery against the target.
// Every behavior asserted here is part of the adapter's dependency surface
// on Plane: if the fake and a live instance both pass, the adapter's unit
// tests against the fake are trustworthy.
func RunConformance(t *testing.T, target ConformanceTarget) {
	t.Helper()
	client := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, target.ProjectID)

	t.Run("ProjectExists", func(t *testing.T) { conformProject(t, client) })
	t.Run("StatesHaveAllGroups", func(t *testing.T) { conformStates(t, client) })
	t.Run("IssueCRUDLifecycle", func(t *testing.T) { conformIssueLifecycle(t, client) })
	t.Run("ExternalIDIdempotency", func(t *testing.T) { conformExternalID(t, client) })
	t.Run("ListPagination", func(t *testing.T) { conformPagination(t, client) })
	t.Run("LabelLifecycle", func(t *testing.T) { conformLabels(t, client) })
	t.Run("CommentLifecycle", func(t *testing.T) { conformComments(t, client) })
	t.Run("ParentChild", func(t *testing.T) { conformParentChild(t, client) })
	t.Run("IdentifierLookup", func(t *testing.T) { conformIdentifierLookup(t, client) })

	if !target.Live {
		t.Run("AuthFailures", func(t *testing.T) { conformAuthFailures(t, target) })
	}
}

func conformProject(t *testing.T, c *plane.Client) {
	p, err := c.GetProject(context.Background())
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.ID == "" || p.Identifier == "" {
		t.Errorf("project missing id/identifier: %+v", p)
	}
	if p.Identifier != strings.ToUpper(p.Identifier) {
		t.Errorf("identifier %q not uppercase (Plane stores identifiers uppercased)", p.Identifier)
	}
}

func conformStates(t *testing.T, c *plane.Client) {
	states, err := c.ListStates(context.Background())
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	groups := map[string]bool{}
	for _, s := range states {
		if s.ID == "" || s.Name == "" {
			t.Errorf("state missing id/name: %+v", s)
		}
		groups[s.Group] = true
	}
	// Every new Plane project carries default states covering these groups.
	for _, g := range []string{"backlog", "unstarted", "started", "completed", "cancelled"} {
		if !groups[g] {
			t.Errorf("no state with group %q (states: %+v)", g, states)
		}
	}
}

func conformIssueLifecycle(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	name := "conformance lifecycle " + uniqueSuffix()

	created, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:            name,
		DescriptionHTML: "<p>created by conformance suite</p>",
		Priority:        "high",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created issue has no ID")
	}
	if created.SequenceID == 0 {
		t.Error("created issue has no sequence_id (server must assign one)")
	}
	if created.StateID == "" {
		t.Error("created issue has no state (server must assign the project default)")
	}
	if created.Priority != "high" {
		t.Errorf("priority = %q, want high", created.Priority)
	}

	// GET round-trip.
	got, err := c.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got == nil || got.Name != name {
		t.Fatalf("GetIssue = %+v, want name %q", got, name)
	}
	if !strings.Contains(got.DescriptionHTML, "created by conformance suite") {
		t.Errorf("description_html = %q, content lost", got.DescriptionHTML)
	}

	// PATCH partial update: name only; priority must survive.
	updated, err := c.UpdateIssue(ctx, created.ID, &plane.IssuePayload{Name: name + " v2"})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.Name != name+" v2" {
		t.Errorf("updated name = %q", updated.Name)
	}
	if updated.Priority != "high" {
		t.Errorf("partial update clobbered priority: %q", updated.Priority)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("updated_at went backwards: %v -> %v", created.UpdatedAt, updated.UpdatedAt)
	}

	// State transition: move to a completed-group state.
	states, err := c.ListStates(ctx)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var doneState string
	for _, s := range states {
		if s.Group == "completed" {
			doneState = s.ID
			break
		}
	}
	if doneState == "" {
		t.Fatal("no completed-group state available")
	}
	closed, err := c.UpdateIssue(ctx, created.ID, &plane.IssuePayload{StateID: doneState})
	if err != nil {
		t.Fatalf("UpdateIssue(state): %v", err)
	}
	if closed.StateID != doneState {
		t.Errorf("state = %q, want %q", closed.StateID, doneState)
	}
	if closed.CompletedAt == nil {
		t.Error("completed_at not set after moving to completed group (server recomputes it on save)")
	}

	// Unknown UUID lookups return nil, nil.
	missing, err := c.GetIssue(ctx, "00000000-0000-0000-0000-00000000dead")
	if err != nil {
		t.Fatalf("GetIssue(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("GetIssue(missing) = %+v, want nil", missing)
	}
}

func conformExternalID(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	externalID := "conformance-" + uniqueSuffix()

	created, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:           "external id " + externalID,
		ExternalID:     externalID,
		ExternalSource: "beads",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.ExternalID != externalID || created.ExternalSource != "beads" {
		t.Errorf("external fields not persisted: %q/%q", created.ExternalID, created.ExternalSource)
	}

	// Duplicate POST with the same pair returns 409 carrying the existing UUID.
	_, err = c.CreateIssue(ctx, &plane.IssuePayload{
		Name:           "duplicate attempt",
		ExternalID:     externalID,
		ExternalSource: "beads",
	})
	var dup *plane.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("duplicate create error = %v (%T), want *DuplicateError", err, err)
	}
	if dup.ExistingID != created.ID {
		t.Errorf("DuplicateError.ExistingID = %q, want %q", dup.ExistingID, created.ID)
	}

	// GET-by-external-id short-circuit returns the single issue object.
	found, err := c.GetIssueByExternalID(ctx, externalID, "beads")
	if err != nil {
		t.Fatalf("GetIssueByExternalID: %v", err)
	}
	if found == nil || found.ID != created.ID {
		t.Errorf("GetIssueByExternalID = %+v, want id %s", found, created.ID)
	}

	// Unknown pair returns nil, nil (404 from the single-object path).
	missing, err := c.GetIssueByExternalID(ctx, "does-not-exist-"+externalID, "beads")
	if err != nil {
		t.Fatalf("GetIssueByExternalID(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("GetIssueByExternalID(missing) = %+v, want nil", missing)
	}
}

func conformPagination(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	marker := "pagination-" + uniqueSuffix()

	// Create enough issues to force at least two pages at per_page=2.
	var createdIDs []string
	for i := 0; i < 5; i++ {
		issue, err := c.CreateIssue(ctx, &plane.IssuePayload{
			Name: fmt.Sprintf("%s #%d", marker, i),
		})
		if err != nil {
			t.Fatalf("CreateIssue #%d: %v", i, err)
		}
		createdIDs = append(createdIDs, issue.ID)
	}

	small := plane.NewClient(c.BaseURL(), apiKeyOf(c), c.Workspace(), c.ProjectID()).WithPerPage(2)
	issues, err := small.ListIssues(ctx, plane.ListIssuesOptions{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}

	seen := map[string]int{}
	for _, is := range issues {
		seen[is.ID]++
	}
	for _, id := range createdIDs {
		if seen[id] == 0 {
			t.Errorf("issue %s missing from paginated list", id)
		}
		if seen[id] > 1 {
			t.Errorf("issue %s appeared %d times (pagination duplicate)", id, seen[id])
		}
	}
}

func conformLabels(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	name := "conf-label-" + uniqueSuffix()

	label, err := c.CreateLabel(ctx, name)
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if label.ID == "" || label.Name != name {
		t.Errorf("label = %+v", label)
	}

	// Duplicate label name returns 409 with the existing UUID.
	_, err = c.CreateLabel(ctx, name)
	var dup *plane.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("duplicate label error = %v (%T), want *DuplicateError", err, err)
	}
	if dup.ExistingID != label.ID {
		t.Errorf("DuplicateError.ExistingID = %q, want %q", dup.ExistingID, label.ID)
	}

	labels, err := c.ListLabels(ctx)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	found := false
	for _, l := range labels {
		if l.ID == label.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("created label %s missing from list", label.ID)
	}

	// Labels attach to issues and persist as UUID arrays.
	issue, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:   "labeled " + name,
		Labels: &[]string{label.ID},
	})
	if err != nil {
		t.Fatalf("CreateIssue(labels): %v", err)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != label.ID {
		t.Errorf("issue.Labels = %v, want [%s]", issue.Labels, label.ID)
	}

	// Replacing with an empty set clears labels (full-replacement semantics).
	cleared, err := c.UpdateIssue(ctx, issue.ID, &plane.IssuePayload{Labels: &[]string{}})
	if err != nil {
		t.Fatalf("UpdateIssue(clear labels): %v", err)
	}
	if len(cleared.Labels) != 0 {
		t.Errorf("labels not cleared: %v", cleared.Labels)
	}
}

func conformComments(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	issue, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "commented " + uniqueSuffix()})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	comment, err := c.CreateComment(ctx, issue.ID, "<p>conformance comment</p>")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if comment.ID == "" {
		t.Error("comment has no ID")
	}

	comments, err := c.ListComments(ctx, issue.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].CommentHTML, "conformance comment") {
		t.Errorf("comments = %+v", comments)
	}
}

func conformParentChild(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	parent, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "parent " + uniqueSuffix()})
	if err != nil {
		t.Fatalf("CreateIssue(parent): %v", err)
	}
	child, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:     "child " + uniqueSuffix(),
		ParentID: parent.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue(child): %v", err)
	}
	if child.ParentID != parent.ID {
		t.Errorf("child.ParentID = %q, want %q", child.ParentID, parent.ID)
	}

	got, err := c.GetIssue(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetIssue(child): %v", err)
	}
	if got.ParentID != parent.ID {
		t.Errorf("fetched child.ParentID = %q, want %q", got.ParentID, parent.ID)
	}
}

func conformIdentifierLookup(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	created, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "identifier lookup " + uniqueSuffix()})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	project, err := c.GetProject(ctx)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}

	identifier := fmt.Sprintf("%s-%d", project.Identifier, created.SequenceID)
	found, err := c.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier(%s): %v", identifier, err)
	}
	if found == nil || found.ID != created.ID {
		t.Errorf("GetIssueByIdentifier(%s) = %+v, want id %s", identifier, found, created.ID)
	}
}

// conformAuthFailures exercises Plane's asymmetric auth errors: 401 for a
// missing key, 403 for an invalid key. Fake-only — probing a live instance
// with bad credentials proves nothing and pollutes audit logs.
func conformAuthFailures(t *testing.T, target ConformanceTarget) {
	ctx := context.Background()

	noKey := plane.NewClient(target.BaseURL, "", target.Workspace, target.ProjectID)
	_, err := noKey.ListStates(ctx)
	var authErr *plane.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("missing key error = %v (%T), want *AuthError", err, err)
	}
	if authErr.StatusCode != 401 {
		t.Errorf("missing key status = %d, want 401", authErr.StatusCode)
	}

	badKey := plane.NewClient(target.BaseURL, "plane_api_wrong", target.Workspace, target.ProjectID)
	_, err = badKey.ListStates(ctx)
	if !errors.As(err, &authErr) {
		t.Fatalf("invalid key error = %v (%T), want *AuthError", err, err)
	}
	if authErr.StatusCode != 403 {
		t.Errorf("invalid key status = %d, want 403 (Plane returns 403 for bad tokens)", authErr.StatusCode)
	}
}

// apiKeyOf extracts the API key from a client for building derived clients.
func apiKeyOf(c *plane.Client) string {
	return c.APIKey()
}
