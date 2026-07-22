package main

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// beads-6pjl6: proxied-server READ helpers for `bd info`.
//
// beads-28ai fixed the reported MODE (resolveInfoMode → "proxied-server"), but
// the DATA blocks — issue_count, config, and the --schema samples — were still
// gated behind `if store != nil`. In proxiedServerMode the global `store` is nil
// (main.go PersistentPreRun wires uowProvider and returns BEFORE
// `store = newDoltStore`), so a hub-connected crew saw the correct mode label but
// a ZERO issue count and NO config map — a populated hub DB looked empty. Same
// read-divergence class as `bd orphans` (beads-ktlo) and `bd config`
// (proxied-routing surface): route the reads through the UOW use-cases when
// proxied, else use the direct store.
//
// GetLocalMetadata (bd_version, used only in the --schema block) is NOT exposed
// on the UOW/domain layer, so schema_version degrades to the existing "unknown"
// default for proxied crew. The primary win — issue_count + config + sample
// issue IDs / detected prefix — routes cleanly through IssueUseCase.SearchIssues
// (returns domain.SearchPage; use .Items) and ConfigUseCase.GetAllConfig/GetConfig.

// infoSearchIssues returns all issues matching filter, from the proxied UOW when
// hub-connected, else the direct global store. Returns (nil, false) when neither
// backend is available or the read fails, so callers can skip the field cleanly.
func infoSearchIssues(ctx context.Context, filter types.IssueFilter) ([]*types.Issue, bool) {
	if usesProxiedServer() {
		if uowProvider == nil {
			return nil, false
		}
		uw, err := uowProvider.NewUOW(ctx)
		if err != nil {
			return nil, false
		}
		defer uw.Close(ctx)
		page, err := uw.IssueUseCase().SearchIssues(ctx, "", filter)
		if err != nil {
			return nil, false
		}
		return page.Items, true
	}
	if store == nil {
		return nil, false
	}
	issues, err := store.SearchIssues(ctx, "", filter)
	if err != nil {
		return nil, false
	}
	return issues, true
}

// infoAllConfig returns the full config map from the proxied UOW when
// hub-connected, else the direct global store. Returns (nil, false) on any miss.
func infoAllConfig(ctx context.Context) (map[string]string, bool) {
	if usesProxiedServer() {
		if uowProvider == nil {
			return nil, false
		}
		uw, err := uowProvider.NewUOW(ctx)
		if err != nil {
			return nil, false
		}
		defer uw.Close(ctx)
		cfg, err := uw.ConfigUseCase().GetAllConfig(ctx)
		if err != nil {
			return nil, false
		}
		return cfg, true
	}
	if store == nil {
		return nil, false
	}
	cfg, err := store.GetAllConfig(ctx)
	if err != nil {
		return nil, false
	}
	return cfg, true
}

// infoConfigValue returns a single config value (best-effort, empty on miss)
// from the proxied UOW when hub-connected, else the direct global store.
func infoConfigValue(ctx context.Context, key string) string {
	if usesProxiedServer() {
		if uowProvider == nil {
			return ""
		}
		uw, err := uowProvider.NewUOW(ctx)
		if err != nil {
			return ""
		}
		defer uw.Close(ctx)
		v, err := uw.ConfigUseCase().GetConfig(ctx, key)
		if err != nil {
			return ""
		}
		return v
	}
	if store == nil {
		return ""
	}
	v, _ := store.GetConfig(ctx, key) // best effort: empty prefix is valid
	return v
}

// infoSchemaVersion returns the bd_version metadata. This lives on the direct
// store's local-metadata surface (GetLocalMetadata), which is NOT exposed on the
// UOW/domain layer, so proxied crew get the graceful "unknown" default rather
// than a nil-panic. Non-proxied crew keep the exact prior behavior.
func infoSchemaVersion(ctx context.Context) string {
	if !usesProxiedServer() && store != nil {
		if v, err := store.GetLocalMetadata(ctx, "bd_version"); err == nil {
			return v
		}
	}
	return "unknown"
}
