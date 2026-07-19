package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

func openConfigProxiedUOW(ctx context.Context) uow.UnitOfWork {
	if uowProvider == nil {
		FatalErrorRespectJSON("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalErrorRespectJSON("open unit of work: %v", err)
	}
	return uw
}

func runConfigSetProxiedServer(ctx context.Context, key, value string) {
	if key == "status.custom" && value != "" {
		if _, err := types.ParseCustomStatusConfig(value); err != nil {
			FatalErrorRespectJSON("invalid status.custom value: %v", err)
		}
	}

	uw := openConfigProxiedUOW(ctx)
	defer uw.Close(ctx)

	if err := uw.ConfigUseCase().SetConfig(ctx, key, value); err != nil {
		FatalErrorRespectJSON("Error setting config: %v", err)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: config set %s", key)); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}

	displayValue := redactUnlessShown(key, value)
	if jsonOutput {
		_ = outputJSON(map[string]string{
			"key":   key,
			"value": displayValue,
		})
	} else {
		fmt.Printf("Set %s = %s\n", key, displayValue)
	}
	printConfigSideEffects(checkConfigSetSideEffects(key, value))
}

func runConfigGetProxiedServer(ctx context.Context, key string) {
	uw := openConfigProxiedUOW(ctx)
	defer uw.Close(ctx)

	value, err := uw.ConfigUseCase().GetConfig(ctx, key)
	if err != nil {
		FatalErrorRespectJSON("Error getting config: %v", err)
	}

	if jsonOutput {
		// beads-aj9r: mirror the direct store branch — GetConfig collapses an
		// absent key to ("", nil), so derive the set/unset signal from
		// GetAllConfig membership for parity with the direct-mode --json shape.
		set := value != ""
		if all, allErr := uw.ConfigUseCase().GetAllConfig(ctx); allErr == nil {
			_, set = all[key]
		}
		_ = outputJSON(map[string]interface{}{
			"key":   key,
			"value": value,
			"set":   set,
		})
		return
	}
	if value == "" {
		fmt.Printf("%s (not set)\n", key)
	} else {
		fmt.Printf("%s\n", value)
	}
}

func runConfigListProxiedServer(ctx context.Context) {
	uw := openConfigProxiedUOW(ctx)
	defer uw.Close(ctx)

	cfg, err := uw.ConfigUseCase().GetAllConfig(ctx)
	if err != nil {
		FatalErrorRespectJSON("Error listing config: %v", err)
	}

	// Redact secret values before any display (text or JSON) — beads-3q1n.
	// --show-secrets opts back into raw.
	for k, v := range cfg {
		cfg[k] = redactUnlessShown(k, v)
	}

	if jsonOutput {
		_ = outputJSON(cfg)
		return
	}

	if len(cfg) == 0 {
		fmt.Println("No configuration set")
		return
	}

	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Println("\nConfiguration:")
	for _, k := range keys {
		fmt.Printf("  %s = %s\n", k, cfg[k])
	}

	showConfigYAMLOverrides(cfg)
}

func runConfigUnsetProxiedServer(ctx context.Context, key string) {
	uw := openConfigProxiedUOW(ctx)
	defer uw.Close(ctx)

	// beads-y3z2: fail loud on a nonexistent key for parity with the direct
	// path (DeleteConfig is idempotent → a false "Unset" otherwise). Membership
	// check, not GetConfig (which can't distinguish absent from empty-valued).
	all, lookupErr := uw.ConfigUseCase().GetAllConfig(ctx)
	if lookupErr != nil {
		FatalErrorRespectJSON("checking config key %s: %v", key, lookupErr)
	}
	if _, exists := all[key]; !exists {
		FatalErrorRespectJSON("no such config key: %s", key)
	}

	if err := uw.ConfigUseCase().DeleteConfig(ctx, key); err != nil {
		FatalErrorRespectJSON("Error deleting config: %v", err)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: config unset %s", key)); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		_ = outputJSON(map[string]string{
			"key": key,
		})
	} else {
		fmt.Printf("Unset %s\n", key)
	}
	printConfigSideEffects(checkConfigUnsetSideEffects(key))
}

func runConfigSetManyProxiedServer(ctx context.Context, keys, values []string) {
	if len(keys) == 0 {
		return
	}
	uw := openConfigProxiedUOW(ctx)
	defer uw.Close(ctx)

	cfgUC := uw.ConfigUseCase()
	for i, k := range keys {
		if err := cfgUC.SetConfig(ctx, k, values[i]); err != nil {
			FatalErrorRespectJSON("Error setting config %s: %v", k, err)
		}
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: config set-many (%d keys)", len(keys))); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}
}
