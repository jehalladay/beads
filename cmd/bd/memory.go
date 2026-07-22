package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage/kvkeys"
)

// memoryPrefix is prepended (after kvPrefix) to all memory keys.
const memoryPrefix = kvkeys.MemoryPrefix

// memoryKeyFlag allows explicit key override for bd remember.
var memoryKeyFlag string

// slugify converts a string to a URL-friendly slug for use as a memory key.
// Takes the first ~8 words, lowercases, replaces non-alphanumeric with hyphens.
func slugify(s string) string {
	s = strings.ToLower(s)
	// Replace non-alphanumeric chars with hyphens
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	// Limit to first ~8 "words" (hyphen-separated segments)
	parts := strings.SplitN(s, "-", 10)
	if len(parts) > 8 {
		parts = parts[:8]
	}
	slug := strings.Join(parts, "-")

	// Cap total length
	if len(slug) > 60 {
		slug = slug[:60]
		// Don't end on a hyphen
		slug = strings.TrimRight(slug, "-")
	}
	return slug
}

// rememberCmd stores a memory.
var rememberCmd = &cobra.Command{
	Use:   `remember "<insight>"`,
	Short: "Store a persistent memory",
	Long: `Store a memory that persists across sessions and account rotations.

Memories are injected at prime time (bd prime) so you have them
in every session without manual loading.

Examples:
  bd remember "always run tests with -race flag"
  bd remember "Dolt phantom DBs hide in three places" --key dolt-phantoms
  bd remember "auth module uses JWT not sessions" --key auth-jwt`,
	GroupID:       "setup",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("remember")

		evt := metrics.NewCommandEvent("remember")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		// beads-21vns: memory is a thin wrapper over the config store (prefixed
		// kv.memory.*); route hub-connected (proxied-server) crew through the UOW
		// like `bd config` instead of failing ensureDirectMode. CLAUDE.md mandates
		// `bd remember` for persistent knowledge — it must work for the fleet.
		if !usesProxiedServer() {
			if err := ensureDirectMode("remember requires direct database access"); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
		}

		insight := args[0]
		if strings.TrimSpace(insight) == "" {
			return HandleErrorRespectJSON("memory content cannot be empty")
		}

		key := memoryKeyFlag
		if key == "" {
			key = slugify(insight)
		}
		if key == "" {
			return HandleErrorRespectJSON("could not generate key from content; use --key to specify one")
		}
		// Reject a key that collides with a wrapWithSchemaVersion-injected JSON
		// key (schema_version / data): `bd memories --json` emits a flat
		// map[string]string keyed by memory key, and wrapping would silently
		// clobber it — data-loss (beads-z0fe, matches bd kv set).
		if kvkeys.IsReservedJSONKey(key) {
			return HandleErrorRespectJSON("key %q is reserved (bd injects it into --json output; it would be silently overwritten). Choose a different key", key)
		}

		storageKey := kvPrefix + memoryPrefix + key

		ctx := rootCtx

		existing, _ := kvGetConfig(ctx, storageKey)
		verb := "Remembered"
		if existing != "" {
			verb = "Updated"
		}

		if err := kvSetConfig(ctx, storageKey, insight, "bd: remember "+key); err != nil {
			return HandleErrorRespectJSON("storing memory: %v", err)
		}
		commandDidWrite.Store(true)

		if jsonOutput {
			return outputJSON(map[string]string{
				"key":    key,
				"value":  insight,
				"action": strings.ToLower(verb),
			})
		}
		fmt.Printf("%s [%s]: %s\n", verb, key, truncateMemory(insight, 80))
		return nil
	},
}

// memoriesCmd lists and searches memories.
var memoriesCmd = &cobra.Command{
	Use:   "memories [search]",
	Short: "List or search persistent memories",
	Long: `List all memories, or search by keyword.

Examples:
  bd memories              # list all memories
  bd memories dolt         # search for memories about dolt
  bd memories "race flag"  # search for a phrase`,
	GroupID:       "setup",
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("memories")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if !usesProxiedServer() {
			if err := ensureDirectMode("memories requires direct database access"); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
		}

		ctx := rootCtx
		allConfig, err := kvGetAllConfig(ctx)
		if err != nil {
			return HandleErrorRespectJSON("listing memories: %v", err)
		}

		// Filter for kv.memory.* keys
		fullPrefix := kvkeys.MemoryConfigKeyPrefix
		memories := make(map[string]string)
		for k, v := range allConfig {
			if strings.HasPrefix(k, fullPrefix) {
				userKey := strings.TrimPrefix(k, fullPrefix)
				memories[userKey] = v
			}
		}

		var search string
		if len(args) > 0 {
			search = strings.ToLower(args[0])
		}
		if search != "" {
			filtered := make(map[string]string)
			for k, v := range memories {
				if strings.Contains(strings.ToLower(k), search) ||
					strings.Contains(strings.ToLower(v), search) {
					filtered[k] = v
				}
			}
			memories = filtered
		}

		if jsonOutput {
			// beads-z0fe read-side leg: warn (non-fatal) if a stored memory key
			// collides with a reserved --json envelope key so its silent clobber
			// on read is visible; value stays readable via `bd recall <key>`.
			warnReservedUserMapKeys(memories, "recall")
			return outputJSON(memories)
		}

		if len(memories) == 0 {
			if search != "" {
				fmt.Printf("No memories matching %q\n", search)
			} else {
				fmt.Println("No memories stored. Use 'bd remember \"insight\"' to add one.")
			}
			return nil
		}

		keys := make([]string, 0, len(memories))
		for k := range memories {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		if search != "" {
			fmt.Printf("Memories matching %q:\n\n", search)
		} else {
			fmt.Printf("Memories (%d):\n\n", len(memories))
		}
		for _, k := range keys {
			v := memories[k]
			fmt.Printf("  %s\n", k)
			fmt.Printf("    %s\n\n", truncateMemory(v, 120))
		}
		return nil
	},
}

// forgetCmd removes a memory.
var forgetCmd = &cobra.Command{
	Use:   "forget <key>",
	Short: "Remove a persistent memory",
	Long: `Remove a memory by its key.

Use 'bd memories' to see available keys.

Examples:
  bd forget dolt-phantoms
  bd forget auth-jwt`,
	GroupID:       "setup",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("forget")

		evt := metrics.NewCommandEvent("forget")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if !usesProxiedServer() {
			if err := ensureDirectMode("forget requires direct database access"); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
		}

		key := args[0]
		storageKey := kvPrefix + memoryPrefix + key

		ctx := rootCtx

		existing, _ := kvGetConfig(ctx, storageKey)
		if existing == "" {
			if jsonOutput {
				// beads-dycj: emit a real JSON boolean, not the string
				// literal "false" — a consumer doing `if result["found"]`
				// reads the non-empty string "false" as truthy and misreads
				// not-found as found. Matches the recall pattern below
				// (memory.go: "found": value != "").
				if jerr := outputJSON(map[string]interface{}{
					"key":   key,
					"found": false,
				}); jerr != nil {
					return jerr
				}
				return SilentExit()
			}
			fmt.Fprintf(os.Stderr, "No memory with key %q\n", key)
			return SilentExit()
		}

		if err := kvDeleteConfig(ctx, storageKey, "bd: forget "+key); err != nil {
			return HandleErrorRespectJSON("forgetting memory: %v", err)
		}
		commandDidWrite.Store(true)

		if jsonOutput {
			// beads-dycj: real JSON boolean, not the string literal "true".
			return outputJSON(map[string]interface{}{
				"key":     key,
				"deleted": true,
			})
		}
		fmt.Printf("Forgot [%s]: %s\n", key, truncateMemory(existing, 80))
		return nil
	},
}

// recallCmd retrieves a specific memory by key.
var recallCmd = &cobra.Command{
	Use:   "recall <key>",
	Short: "Retrieve a specific memory",
	Long: `Retrieve the full content of a memory by its key.

Examples:
  bd recall dolt-phantoms
  bd recall auth-jwt`,
	GroupID:       "setup",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("recall")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if !usesProxiedServer() {
			if err := ensureDirectMode("recall requires direct database access"); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
		}

		key := args[0]
		storageKey := kvPrefix + memoryPrefix + key

		ctx := rootCtx
		value, err := kvGetConfig(ctx, storageKey)
		if err != nil {
			return HandleErrorRespectJSON("recalling memory: %v", err)
		}

		if jsonOutput {
			// beads-7fgq (sibling of 7qkq on the memory-get path): a missing key
			// is a RESULT, not an error, in --json mode — the found:false field
			// already communicates the miss. Return rc0 so `bd recall k --json`
			// matches the sibling `bd config get k --json` (set:false at rc0);
			// failing the exit code on a successful-but-empty lookup is a
			// redundant cross-command divergence that trips scripted
			// `bd recall k --json; test $? …`. The TEXT branch below keeps rc1
			// for shell ergonomics (`bd recall k || default`).
			return outputJSON(map[string]interface{}{
				"key":   key,
				"value": value,
				"found": value != "",
			})
		}
		if value == "" {
			fmt.Fprintf(os.Stderr, "No memory with key %q\n", key)
			return SilentExit()
		}
		fmt.Printf("%s\n", value)
		return nil
	},
}

// truncateMemory shortens a string to maxLen for display.
func truncateMemory(s string, maxLen int) string {
	// Replace newlines with spaces for single-line display
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func init() {
	rememberCmd.Flags().StringVar(&memoryKeyFlag, "key", "", "Explicit key for the memory (auto-generated from content if not set). If a memory with this key already exists, it will be updated in place")

	rootCmd.AddCommand(rememberCmd)
	rootCmd.AddCommand(memoriesCmd)
	rootCmd.AddCommand(forgetCmd)
	rootCmd.AddCommand(recallCmd)
}
