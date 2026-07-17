package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

func parseDepSpecs(deps []string) ([]domain.DependencySpec, error) {
	var out []domain.DependencySpec
	for _, raw := range deps {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		spec, err := parseDepSpec(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}

func parseDepSpec(raw string) (domain.DependencySpec, error) {
	if !strings.Contains(raw, ":") {
		return domain.DependencySpec{
			Type:     types.DepBlocks,
			TargetID: raw,
		}, nil
	}

	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return domain.DependencySpec{}, fmt.Errorf("invalid dependency format %q, expected 'type:id' or 'id'", raw)
	}
	rawType := types.DependencyType(strings.TrimSpace(parts[0]))
	target := strings.TrimSpace(parts[1])

	spec := domain.DependencySpec{TargetID: target}
	switch rawType {
	case "depends-on", "blocked-by":
		spec.Type = types.DepBlocks
	case types.DepBlocks:
		spec.Type = types.DepBlocks
		spec.SwapDirection = true
	default:
		spec.Type = rawType
	}

	if !spec.Type.IsValid() {
		return domain.DependencySpec{}, fmt.Errorf("invalid dependency type %q (must be non-empty, max 32 chars); valid types: %s",
			spec.Type, createDepsAcceptedTypeList())
	}
	if !spec.Type.IsWellKnown() {
		return domain.DependencySpec{}, fmt.Errorf("unknown dependency type %q; valid types: %s",
			spec.Type, createDepsAcceptedTypeList())
	}
	return spec, nil
}

func buildWaitsFor(spawnerID, gate string) (*domain.WaitsForSpec, error) {
	spawnerID = strings.TrimSpace(spawnerID)
	if spawnerID == "" {
		return nil, nil
	}
	if gate == "" {
		gate = types.WaitsForAllChildren
	}
	if gate != types.WaitsForAllChildren && gate != types.WaitsForAnyChildren {
		return nil, fmt.Errorf("invalid --waits-for-gate value %q (valid: all-children, any-children)", gate)
	}
	return &domain.WaitsForSpec{SpawnerID: spawnerID, Gate: gate}, nil
}

func discoveredFromParent(deps []string) string {
	for _, raw := range deps {
		raw = strings.TrimSpace(raw)
		if raw == "" || !strings.Contains(raw, ":") {
			continue
		}
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) != 2 {
			continue
		}
		depType := types.DependencyType(strings.TrimSpace(parts[0]))
		target := strings.TrimSpace(parts[1])
		if depType == types.DepDiscoveredFrom && target != "" {
			return target
		}
	}
	return ""
}

func overlayYAMLPrefix(dbPrefix string) string {
	if v := strings.TrimSpace(config.GetString("issue-prefix")); v != "" {
		return v
	}
	return dbPrefix
}

// resolvePrefixValidation resolves the (authoritative dbPrefix, allowedPrefixes)
// pair used to validate an explicit --id, honoring BOTH the live DB prefix and
// a config.yaml issue-prefix (beads-xevo).
//
// The live DB prefix (issue_counter) is authoritative — it is the prefix every
// auto-generated id actually carries — so it stays the dbPrefix and is always
// accepted. A YAML issue-prefix that DISAGREES with the DB prefix is folded into
// the allowed-list (union-accept) rather than REPLACING the DB prefix, which is
// what the old overlayYAMLPrefix did: a stale config.yaml prefix shadowed the DB
// prefix so `bd create --id <db-prefix>-x` was rejected on the DB's OWN prefix
// while `--id <yaml-prefix>-x` (a prefix no real bead uses) succeeded — a
// gen-vs-validation split-brain. Union-accept fixes it without surprising an
// intentional YAML override (both prefixes work).
//
// When the DB prefix is empty (un-inited store) it falls back to the YAML prefix
// as the dbPrefix, preserving the prior behavior for that case.
func resolvePrefixValidation(dbPrefix, allowedFromDB string) (string, string) {
	dbPrefix = strings.TrimSpace(dbPrefix)
	yamlPrefix := strings.TrimSpace(config.GetString("issue-prefix"))

	// Un-inited store: no authoritative DB prefix, so YAML is all we have.
	if dbPrefix == "" {
		return yamlPrefix, allowedFromDB
	}

	// DB prefix is authoritative. Fold a disagreeing YAML prefix into the
	// allowed-list so ids matching either are accepted.
	allowed := allowedFromDB
	if yamlPrefix != "" && yamlPrefix != dbPrefix {
		if allowed == "" {
			allowed = yamlPrefix
		} else {
			allowed = allowed + "," + yamlPrefix
		}
	}
	return dbPrefix, allowed
}
