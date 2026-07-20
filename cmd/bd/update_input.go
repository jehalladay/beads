package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/timeparsing"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/validation"
)

type updateInput struct {
	fields           map[string]any
	addLabels        []string
	removeLabels     []string
	setLabels        *[]string
	reparent         *string
	claim            bool
	appendNotes      string
	hasAppendNotes   bool
	setMetadata      []string
	unsetMetadata    []string
	mergeMetadataIn  json.RawMessage
	clearDeferStatus bool
}

func gatherUpdateInput(ctx context.Context, cmd *cobra.Command) *updateInput {
	in := &updateInput{fields: map[string]any{}}

	if cmd.Flags().Changed("status") {
		status, _ := cmd.Flags().GetString("status")
		validateUpdateStatus(ctx, status)
		in.fields["status"] = status
		if status == "closed" {
			session, _ := cmd.Flags().GetString("session")
			if session == "" {
				session = os.Getenv("CLAUDE_SESSION_ID")
			}
			if session != "" {
				in.fields["closed_by_session"] = session
			}
		}
	}
	if cmd.Flags().Changed("priority") {
		priorityStr, _ := cmd.Flags().GetString("priority")
		priority, err := validation.ValidatePriority(priorityStr)
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		in.fields["priority"] = priority
	}
	if cmd.Flags().Changed("title") {
		title, _ := cmd.Flags().GetString("title")
		title = strings.TrimSpace(title)
		if title == "" {
			FatalErrorRespectJSON("title cannot be empty")
		}
		in.fields["title"] = title
	}
	if cmd.Flags().Changed("assignee") {
		assignee, _ := cmd.Flags().GetString("assignee")
		// Trim + fold "none" through the shared normalizer so the proxied
		// update path stores the canonical form the read/filter side matches
		// (beads-llzt); mirrors the live update.go path and create_input.go.
		in.fields["assignee"] = normalizeAssignee(assignee)
	}
	description, descChanged, err := getDescriptionFlag(cmd)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}
	if descChanged {
		if err := validateDescriptionUpdate(cmd, description, descChanged); err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		in.fields["description"] = description
	}
	design, designChanged, err := getDesignFlag(cmd)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}
	if designChanged {
		in.fields["design"] = design
	}
	if cmd.Flags().Changed("notes") && cmd.Flags().Changed("append-notes") {
		FatalErrorRespectJSON("cannot specify both --notes and --append-notes")
	}
	if cmd.Flags().Changed("notes") {
		notes, _ := cmd.Flags().GetString("notes")
		in.fields["notes"] = notes
	}
	if cmd.Flags().Changed("append-notes") {
		in.appendNotes, _ = cmd.Flags().GetString("append-notes")
		// Reject whitespace-only append text on the proxied path too (mirrors the
		// direct update.go guard and the --title guard above) so a blank note is
		// never silently stored regardless of server mode (beads-beln6). The raw
		// value is preserved on write; only an all-whitespace value is rejected.
		if strings.TrimSpace(in.appendNotes) == "" {
			FatalErrorRespectJSON("append-notes text cannot be empty")
		}
		in.hasAppendNotes = true
	}
	if cmd.Flags().Changed("acceptance") || cmd.Flags().Changed("acceptance-criteria") {
		var ac string
		if cmd.Flags().Changed("acceptance") {
			ac, _ = cmd.Flags().GetString("acceptance")
		} else {
			ac, _ = cmd.Flags().GetString("acceptance-criteria")
		}
		in.fields["acceptance_criteria"] = ac
	}
	if cmd.Flags().Changed("external-ref") {
		externalRef, _ := cmd.Flags().GetString("external-ref")
		if externalRef == "" {
			in.fields["external_ref"] = nil
		} else {
			in.fields["external_ref"] = externalRef
		}
	}
	if cmd.Flags().Changed("spec-id") {
		specID, _ := cmd.Flags().GetString("spec-id")
		in.fields["spec_id"] = specID
	}
	if cmd.Flags().Changed("estimate") {
		estimate, _ := cmd.Flags().GetInt("estimate")
		if estimate < 0 {
			FatalErrorRespectJSON("estimate must be a non-negative number of minutes")
		}
		in.fields["estimated_minutes"] = estimate
	}
	if cmd.Flags().Changed("type") {
		issueType, _ := cmd.Flags().GetString("type")
		in.fields["issue_type"] = utils.NormalizeIssueType(issueType)
	}
	if cmd.Flags().Changed("add-label") {
		in.addLabels, _ = cmd.Flags().GetStringSlice("add-label")
	}
	if cmd.Flags().Changed("remove-label") {
		in.removeLabels, _ = cmd.Flags().GetStringSlice("remove-label")
	}
	if cmd.Flags().Changed("set-labels") {
		labels, _ := cmd.Flags().GetStringSlice("set-labels")
		in.setLabels = &labels
	}
	if cmd.Flags().Changed("parent") {
		parent, _ := cmd.Flags().GetString("parent")
		in.reparent = &parent
	}
	if cmd.Flags().Changed("await-id") {
		awaitID, _ := cmd.Flags().GetString("await-id")
		in.fields["await_id"] = awaitID
	}
	if cmd.Flags().Changed("due") {
		dueStr, _ := cmd.Flags().GetString("due")
		if dueStr == "" {
			in.fields["due_at"] = nil
		} else {
			t, err := timeparsing.ParseRelativeTime(dueStr, time.Now())
			if err != nil {
				FatalErrorRespectJSON("invalid --due format %q. Examples: +6h, tomorrow, next monday, 2025-01-15", dueStr)
			}
			in.fields["due_at"] = t
		}
	}
	if cmd.Flags().Changed("defer") {
		deferStr, _ := cmd.Flags().GetString("defer")
		jsonOut, _ := cmd.Flags().GetBool("json")
		if deferStr == "" {
			in.fields["defer_until"] = nil
			if _, ok := in.fields["status"]; !ok {
				in.clearDeferStatus = true
			}
		} else {
			t, err := timeparsing.ParseRelativeTime(deferStr, time.Now())
			if err != nil {
				FatalErrorRespectJSON("invalid --defer format %q. Examples: +1h, tomorrow, next monday, 2025-01-15", deferStr)
			}
			inPast := t.Before(time.Now())
			if inPast && !jsonOut {
				fmt.Fprintf(os.Stderr, "%s Defer date %q is in the past. Issue will appear in bd ready immediately.\n",
					ui.RenderWarn("!"), t.Format("2006-01-02 15:04"))
				fmt.Fprintf(os.Stderr, "  Did you mean a future date? Use --defer=+1h or --defer=tomorrow\n")
			}
			in.fields["defer_until"] = t
			if _, ok := in.fields["status"]; !ok && !inPast {
				in.fields["status"] = string(types.StatusDeferred)
			}
		}
	}
	ephemeralChanged := cmd.Flags().Changed("ephemeral")
	persistentChanged := cmd.Flags().Changed("persistent")
	noHistoryChanged := cmd.Flags().Changed("no-history")
	historyChanged := cmd.Flags().Changed("history")
	if ephemeralChanged && persistentChanged {
		FatalErrorRespectJSON("cannot specify both --ephemeral and --persistent flags")
	}
	if noHistoryChanged && ephemeralChanged {
		FatalErrorRespectJSON("cannot specify both --no-history and --ephemeral flags")
	}
	if noHistoryChanged && historyChanged {
		FatalErrorRespectJSON("cannot specify both --no-history and --history flags")
	}
	if ephemeralChanged {
		in.fields["wisp"] = true
	}
	if persistentChanged {
		in.fields["wisp"] = false
	}
	if noHistoryChanged {
		in.fields["no_history"] = true
	}
	if historyChanged {
		in.fields["no_history"] = false
	}
	// beads-n79c: --pinned/--no-pinned set the Issue.Pinned context-marker bool
	// (beads-9ynk), distinct from status="pinned". The DIRECT update.go path
	// captured these but the shared gatherUpdateInput (used by the proxied
	// server) did not, so `bd update --pinned`/`--no-pinned` was a silent no-op
	// over the proxied path. Mirror the direct path's both-flags guard.
	pinnedChanged := cmd.Flags().Changed("pinned")
	noPinnedChanged := cmd.Flags().Changed("no-pinned")
	if pinnedChanged && noPinnedChanged {
		FatalErrorRespectJSON("cannot specify both --pinned and --no-pinned flags")
	}
	if pinnedChanged {
		in.fields["pinned"] = true
	}
	if noPinnedChanged {
		in.fields["pinned"] = false
	}
	if cmd.Flags().Changed("metadata") {
		metadataValue, _ := cmd.Flags().GetString("metadata")
		var metadataJSON string
		if strings.HasPrefix(metadataValue, "@") {
			filePath := metadataValue[1:]
			data, err := os.ReadFile(filePath) //#nosec G304 -- user-supplied path via @file syntax
			if err != nil {
				FatalErrorRespectJSON("failed to read metadata file %s: %v", filePath, err)
			}
			metadataJSON = string(data)
		} else {
			metadataJSON = metadataValue
		}
		if !json.Valid([]byte(metadataJSON)) {
			FatalErrorRespectJSON("invalid JSON in --metadata: must be valid JSON")
		}
		if !metadataIsJSONObject(metadataJSON) {
			FatalErrorRespectJSON(`--metadata must be a JSON object, e.g. {"key":"value"} (arrays and scalars can't be edited by --set-metadata/--unset-metadata)`)
		}
		in.mergeMetadataIn = json.RawMessage(metadataJSON)
	}
	setMetadataFlags, _ := cmd.Flags().GetStringArray("set-metadata")
	unsetMetadataFlags, _ := cmd.Flags().GetStringArray("unset-metadata")
	if (len(setMetadataFlags) > 0 || len(unsetMetadataFlags) > 0) && cmd.Flags().Changed("metadata") {
		FatalErrorRespectJSON("cannot combine --metadata with --set-metadata or --unset-metadata")
	}
	in.setMetadata = setMetadataFlags
	in.unsetMetadata = unsetMetadataFlags

	in.claim, _ = cmd.Flags().GetBool("claim")
	return in
}

func validateUpdateStatus(ctx context.Context, status string) {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalError("open unit of work: %v", err)
	}
	names, err := uw.ConfigUseCase().ListAllStatusNames(ctx)
	uw.Close(ctx)
	if err != nil {
		FatalErrorRespectJSON("read status set: %v", err)
	}
	for _, name := range names {
		if name == status {
			return
		}
	}
	FatalErrorRespectJSON("invalid status %q (allowed: %s)", status, strings.Join(names, ", "))
}

func isUpdateInputNoop(in *updateInput) bool {
	if in.claim {
		return false
	}
	if len(in.fields) > 0 || in.hasAppendNotes || in.setLabels != nil || in.reparent != nil {
		return false
	}
	if len(in.addLabels) > 0 || len(in.removeLabels) > 0 {
		return false
	}
	if len(in.mergeMetadataIn) > 0 || len(in.setMetadata) > 0 || len(in.unsetMetadata) > 0 {
		return false
	}
	return true
}
