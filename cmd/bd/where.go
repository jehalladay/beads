package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/utils"
)

// WhereResult contains information about the active beads location
type WhereResult struct {
	Path           string `json:"path"`                      // Active .beads directory path
	RedirectedFrom string `json:"redirected_from,omitempty"` // Original path if redirected
	Prefix         string `json:"prefix,omitempty"`          // Issue prefix (if detectable)
	DatabasePath   string `json:"database_path,omitempty"`   // Full path to database file
	StorePrefix    string `json:"store_prefix,omitempty"`    // Live store issue_prefix, when it differs from Prefix (drift)
	PrefixStale    bool   `json:"prefix_stale,omitempty"`    // True when the YAML Prefix disagrees with the live StorePrefix
}

var whereCmd = &cobra.Command{
	Use:     "where",
	Args:    cobra.NoArgs, // beads-7pnnd: reject stray positionals with a clean usage error
	GroupID: "setup",
	Short:   "Show active beads location",
	Long: `Show the active beads database location, including redirect information.

	This command is useful for debugging when using redirects, to understand
	which beads workspace is actually being used.

Examples:
  bd where           # Show active beads location
  bd where --json    # Output in JSON format
`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("where")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		result := WhereResult{}

		if selected := selectedNoDBBeadsDir(cmd); selected != "" {
			prepareSelectedNoDBContext(selected)
		}

		beadsDir := resolveWhereBeadsDir(cmd)
		if beadsDir == "" {
			if jsonOutput {
				if jerr := outputJSON(map[string]string{
					"error":   "no_beads_directory",
					"message": activeWorkspaceNotFoundMessage(),
					"hint":    whereDiagHint(),
				}); jerr != nil {
					return jerr
				}
				return SilentExit()
			}
			return HandleErrorWithHint(activeWorkspaceNotFoundMessage(), whereDiagHint())
		}

		result.Path = beadsDir

		// Check if we got here via redirect by looking for the original .beads directory
		// Walk up from cwd to find any .beads with a redirect file
		originalBeadsDir := findOriginalBeadsDir()
		if originalBeadsDir != "" && originalBeadsDir != beadsDir {
			result.RedirectedFrom = originalBeadsDir
		}

		// Find the database path
		dbPath := resolveWhereDatabasePath()
		if dbPath != "" {
			result.DatabasePath = dbPath
		}

		// Prefer the active workspace YAML when available. Avoid process-global
		// config here because `bd where` may be reporting a workspace selected
		// by BEADS_DB/BEADS_DIR rather than the caller's current repository.
		yamlPrefix := config.GetStringFromDir(beadsDir, "issue-prefix")
		if yamlPrefix == "" {
			yamlPrefix = config.GetStringFromDir(beadsDir, "issue_prefix")
		}
		if yamlPrefix != "" {
			result.Prefix = yamlPrefix
			// A diagnostic that silently trusts a stale YAML hint defeats its
			// own purpose. When the live store is cheaply reachable, compare
			// and surface any drift (beads-m08v) without changing precedence.
			if dbPath != "" {
				if storePrefix := readStorePrefixBestEffort(dbPath); storePrefix != "" && storePrefix != yamlPrefix {
					result.StorePrefix = storePrefix
					result.PrefixStale = true
				}
			}
		} else if dbPath != "" && shouldReadWherePrefixFromStore(beadsDir) {
			_ = withStorage(getRootContext(), nil, dbPath, func(currentStore storage.DoltStorage) error {
				prefix, err := currentStore.GetConfig(getRootContext(), "issue_prefix")
				if err == nil && prefix != "" {
					result.Prefix = prefix
				}
				return nil
			})
		}

		if jsonOutput {
			return outputJSON(result)
		}
		fmt.Println(result.Path)
		if result.RedirectedFrom != "" {
			fmt.Printf("  (via redirect from %s)\n", result.RedirectedFrom)
		}
		if result.Prefix != "" {
			if result.PrefixStale {
				fmt.Printf("  prefix: %s (config.yaml) ⚠ store: %s — STALE config.yaml\n", result.Prefix, result.StorePrefix)
			} else {
				fmt.Printf("  prefix: %s\n", result.Prefix)
			}
		}
		if result.DatabasePath != "" {
			fmt.Printf("  database: %s\n", result.DatabasePath)
		}
		return nil
	},
}

func resolveWhereBeadsDir(cmd *cobra.Command) string {
	if selected := selectedNoDBBeadsDir(cmd); selected != "" {
		return selected
	}

	return beads.FindBeadsDir()
}

func resolveWhereDatabasePath() string {
	return beads.FindDatabasePath()
}

// readStorePrefixBestEffort reads the live store's issue_prefix for drift
// detection only. It is strictly best-effort: any error (server down,
// proxied-server mode, timeout) yields "" and the caller keeps the YAML value.
// A short timeout bounds the read so `bd where` can never hang on an
// unreachable server (guards against the gt-hook/dolt-config hang class).
func readStorePrefixBestEffort(dbPath string) string {
	ctx, cancel := context.WithTimeout(getRootContext(), 3*time.Second)
	defer cancel()

	var storePrefix string
	_ = withStorage(ctx, nil, dbPath, func(currentStore storage.DoltStorage) error {
		prefix, err := currentStore.GetConfig(ctx, "issue_prefix")
		if err == nil {
			storePrefix = prefix
		}
		return nil
	})
	return storePrefix
}

func shouldReadWherePrefixFromStore(beadsDir string) bool {
	if beadsDir == "" {
		return false
	}

	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil {
		return true
	}

	// `bd where` should be able to report selected metadata without requiring
	// a live Dolt server (or spawning the proxied-server daemon) just to
	// recover issue_prefix.
	return !cfg.IsDoltServerMode() && !cfg.IsDoltProxiedServerMode()
}

// findOriginalBeadsDir walks up from cwd looking for a .beads directory with a redirect file
// Returns the original .beads path if found, empty string otherwise
func findOriginalBeadsDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Canonicalize cwd to handle symlinks
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	// Check BEADS_DIR first: if the env points at a .beads directory with a
	// redirect file, that's the original. Fall through to the fs walk if
	// BEADS_DIR is set but does not contain a redirect — bd's startup now
	// rebinds BEADS_DIR to the resolved target (#3230) after following
	// redirects, so an unconditional early-return here would hide every
	// redirect from `bd where` output.
	if envDir := os.Getenv("BEADS_DIR"); envDir != "" {
		envDir = utils.CanonicalizePath(envDir)
		redirectFile := filepath.Join(envDir, beads.RedirectFileName)
		if _, err := os.Stat(redirectFile); err == nil {
			return envDir
		}
	}

	// Walk up directory tree looking for .beads with redirect
	for dir := cwd; dir != "/" && dir != "."; {
		beadsDir := filepath.Join(dir, ".beads")
		if info, err := os.Stat(beadsDir); err == nil && info.IsDir() {
			redirectFile := filepath.Join(beadsDir, beads.RedirectFileName)
			if _, err := os.Stat(redirectFile); err == nil {
				return beadsDir
			}
			// Found .beads without redirect - this is the actual location
			return ""
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root (works on both Unix and Windows)
			// On Unix: filepath.Dir("/") returns "/"
			// On Windows: filepath.Dir("C:\\") returns "C:\\"
			break
		}
		dir = parent
	}

	return ""
}

func init() {
	rootCmd.AddCommand(whereCmd)
}
