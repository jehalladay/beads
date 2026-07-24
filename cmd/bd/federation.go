//go:build cgo

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/ui"
	"golang.org/x/term"
)

var (
	federationPeer     string
	federationStrategy string
	federationUser     string
	federationPassword string
	federationSov      string
)

var federationCmd = &cobra.Command{
	Use:     "federation",
	GroupID: "sync",
	Short:   "Manage peer-to-peer federation with other workspaces",
	Long: `Manage peer-to-peer federation between Dolt-backed beads databases.

Federation enables synchronized issue tracking across multiple workspaces,
each maintaining their own Dolt database while sharing updates via remotes.

Requires the Dolt storage backend.`,
}

var federationSyncCmd = &cobra.Command{
	Use:   "sync [--peer name]",
	Short: "Synchronize with a peer town",
	Long: `Pull from and push to peer towns.

Without --peer, syncs with all configured peers.
With --peer, syncs only with the specified peer.

Handles merge conflicts using the configured strategy:
  --strategy ours    Keep local changes on conflict
  --strategy theirs  Accept remote changes on conflict

If no strategy is specified and conflicts occur, the sync will pause
and report which tables have conflicts for manual resolution.

Examples:
  bd federation sync                      # Sync with all peers
  bd federation sync --peer town-beta     # Sync with specific peer
  bd federation sync --strategy theirs    # Auto-resolve using remote values`,
	Args:          cobra.NoArgs, // beads-wy9jc: reject stray positionals ([--peer name] is a flag, RunE reads no args)
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runFederationSync,
}

var federationStatusCmd = &cobra.Command{
	Use:   "status [--peer name]",
	Short: "Show federation sync status",
	Long: `Show synchronization status with peer towns.

Displays:
  - Configured peers and their URLs
  - Commits ahead/behind each peer
  - Whether there are unresolved conflicts

Examples:
  bd federation status                    # Status for all peers
  bd federation status --peer town-beta   # Status for specific peer`,
	Args:          cobra.NoArgs, // beads-wy9jc: reject stray positionals ([--peer name] is a flag, RunE reads no args)
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runFederationStatus,
}

var federationAddPeerCmd = &cobra.Command{
	Use:   "add-peer <name> <url>",
	Short: "Add a federation peer with optional SQL credentials",
	Long: `Add a new federation peer remote with optional SQL user authentication.

The URL can be:
  - dolthub://org/repo      DoltHub hosted repository
  - host:port/database      Direct dolt sql-server connection
  - file:///path/to/repo    Local file path (for testing)

Credentials are encrypted and stored locally. They are used automatically
when syncing with the peer. If --user is provided without --password,
you will be prompted for the password interactively.

Examples:
  bd federation add-peer town-beta dolthub://acme/town-beta-beads
  bd federation add-peer town-gamma 192.168.1.100:3306/beads --user sync-bot
  bd federation add-peer partner https://partner.example.com/beads --user admin --password secret`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runFederationAddPeer,
}

var federationRemovePeerCmd = &cobra.Command{
	Use:           "remove-peer <name>",
	Short:         "Remove a federation peer",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runFederationRemovePeer,
}

var federationListPeersCmd = &cobra.Command{
	Use:           "list-peers",
	Args:          cobra.NoArgs, // beads-8jy7e: reject stray positionals with a clean usage error
	Short:         "List configured federation peers",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runFederationListPeers,
}

func init() {
	// Add subcommands
	federationCmd.AddCommand(federationSyncCmd)
	federationCmd.AddCommand(federationStatusCmd)
	federationCmd.AddCommand(federationAddPeerCmd)
	federationCmd.AddCommand(federationRemovePeerCmd)
	federationCmd.AddCommand(federationListPeersCmd)

	// Flags for sync
	federationSyncCmd.Flags().StringVar(&federationPeer, "peer", "", "Specific peer to sync with")
	federationSyncCmd.Flags().StringVar(&federationStrategy, "strategy", "", "Conflict resolution strategy (ours|theirs)")

	// Flags for status
	federationStatusCmd.Flags().StringVar(&federationPeer, "peer", "", "Specific peer to check")

	// Flags for add-peer (SQL user authentication)
	federationAddPeerCmd.Flags().StringVarP(&federationUser, "user", "u", "", "SQL username for authentication")
	federationAddPeerCmd.Flags().StringVarP(&federationPassword, "password", "p", "", "SQL password (prompted if --user set without --password)")
	federationAddPeerCmd.Flags().StringVar(&federationSov, "sovereignty", "", "Sovereignty tier (T1, T2, T3, T4)")

	rootCmd.AddCommand(federationCmd)
}

func getFederatedStore() (storage.DoltStorage, error) {
	if store == nil {
		return nil, fmt.Errorf("no store available")
	}
	return store, nil
}

// requireDirectFederation fails loud when federation is run in proxied-server
// mode (beads-mgjco, aocj fail-loud class). main.go's PersistentPreRun returns
// early in proxied mode (main.go:1155) leaving the global store nil, so the
// federation handlers' direct store.AddFederationPeer/AddRemote/RemoveRemote/
// ListRemotes calls (and getFederatedStore().Sync) either nil-panic or
// misdiagnose the failure as a local "no store available". Federation's
// headline is credentialed peer management + Sync, which live only on the
// concrete DoltStore (credentials.go uses a raw *sql.Tx and credential
// encryption; there is no proxied/UOW federation path), so refuse the whole
// family with an accurate message (mirrors requireDirectMergeSlot / restore).
func requireDirectFederation(op string) error {
	if usesProxiedServer() {
		return HandleErrorRespectJSON("federation %s is not supported in proxied-server mode (connect directly with an embedded/dolt store)", op)
	}
	if err := ensureStoreActive(); err != nil {
		return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
	}
	return nil
}

// populateSyncResultErrorMsgs mirrors a SyncResult's error-typed fields into
// their JSON-visible string twins so `bd federation sync --json` carries the
// failure signal. Both SyncResult.Error and SyncResult.PushError are `json:"-"`
// (an `error` marshals to null/`{}`, carrying no useful JSON), so without this
// the --json payload silently drops them.
//
//   - fatalErr (the error returned alongside the result) → ErrorMsg
//     (`json:"error"`): the FATAL per-peer sync failure (beads-o35h0). Pass nil
//     when Sync returned no error.
//   - result.PushError → PushErrorMsg (`json:"push_error"`): the NON-fatal push
//     failure (beads-00oy4) — a peer merge that succeeded but whose follow-up
//     push was rejected. The human path shows "○ Push skipped: <err>"; this
//     restores the same signal for structured consumers. It does NOT affect the
//     exit code (a push failure is non-fatal by design; RC stays 0).
//
// Existing non-empty *Msg values are preserved (a backend may populate them).
func populateSyncResultErrorMsgs(result *storage.SyncResult, fatalErr error) {
	if result == nil {
		return
	}
	if fatalErr != nil && result.ErrorMsg == "" {
		result.ErrorMsg = fatalErr.Error()
	}
	if result.PushError != nil && result.PushErrorMsg == "" {
		result.PushErrorMsg = result.PushError.Error()
	}
}

// printFederationDivergedGuidance prints recovery guidance (to stderr) when a
// federation peer merge fails with diverged histories — the two towns have
// independent commit histories with no common merge base (beads-sa08u). Unlike
// bd dolt push/pull's diverged guidance, the recovery here is peer↔peer: there
// is no single "origin" to re-clone or force-push to, so the towns must be
// re-converged by seeding one from the other's export.
func printFederationDivergedGuidance(peer string) {
	w := os.Stderr
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Federation sync with %q failed: the two towns have diverged\n", peer)
	fmt.Fprintln(w, "histories with no common merge base. This happens when each town was")
	fmt.Fprintln(w, "initialized independently (both ran 'bd init') instead of one being")
	fmt.Fprintln(w, "cloned from the other — so there is no shared ancestor to merge against.")
	fmt.Fprintln(w, "Retrying will not help; the histories cannot be auto-merged.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Recovery (re-converge on one canonical town):")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  1. Pick ONE town as canonical. On EACH other town, save local-only")
	fmt.Fprintln(w, "     issues so nothing is lost:")
	fmt.Fprintln(w, "       bd export --all -o /tmp/beads-local.jsonl")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  2. Re-seed each non-canonical town by cloning the canonical town's")
	fmt.Fprintln(w, "     database (shared history), then re-apply the saved issues:")
	fmt.Fprintln(w, "       bd import /tmp/beads-local.jsonl")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Tip: to avoid this, federate towns that share history — seed a new town")
	fmt.Fprintln(w, "by cloning an existing one rather than running 'bd init' on each.")
}

// printFederationPKMismatchGuidance prints recovery guidance (to stderr) when a
// federation peer merge is refused because a table's primary key set differs
// across the merging histories or their common ancestor (beads-y19rc) — the
// #4259 schema-fork geometry, where two towns upgraded bd independently across
// a PK-reshaping migration while un-synced changes existed on both sides.
// Retrying never converges. Unlike bd dolt push/pull's PK-mismatch guidance
// (which hands out `bd dolt push --force` / `bd bootstrap`, correct for a
// local↔remote-origin fork), the federation recovery is peer↔peer: there is no
// single origin to force-push to, so re-converge by seeding each town from one
// canonical town's export — mirroring printFederationDivergedGuidance.
func printFederationPKMismatchGuidance(peer string, err error) {
	w := os.Stderr
	table := ancestorPKMismatchTable(err)
	fmt.Fprintln(w, "")
	if table != "" {
		fmt.Fprintf(w, "Federation sync with %q was refused: table %q has different\n", peer, table)
	} else {
		fmt.Fprintf(w, "Federation sync with %q was refused: a table has different\n", peer)
	}
	fmt.Fprintln(w, "primary keys across the two towns' histories (or their common ancestor).")
	fmt.Fprintln(w, "This is a schema fork: the towns upgraded bd (and ran schema migrations)")
	fmt.Fprintln(w, "independently while un-synced changes existed on both sides.")
	fmt.Fprintln(w, "Retrying will not help; these histories can no longer be merged.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Recovery (re-converge on one canonical town):")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  1. Pick ONE town as canonical (usually the most complete/up-to-date)")
	fmt.Fprintln(w, "     and upgrade bd there. On EACH other town, save local-only issues:")
	fmt.Fprintln(w, "       bd export --all -o /tmp/beads-local.jsonl")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  2. Re-seed each non-canonical town by cloning the canonical town's")
	fmt.Fprintln(w, "     database (shared history), then re-apply the saved issues:")
	fmt.Fprintln(w, "       bd import /tmp/beads-local.jsonl")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Tip: to avoid this, upgrade bd on one town and re-seed the others from it")
	fmt.Fprintln(w, "rather than upgrading each independently.")
}

func runFederationSync(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("federation-sync")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	if err := requireDirectFederation("sync"); err != nil {
		return err
	}

	ds, err := getFederatedStore()
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	if federationStrategy != "" && federationStrategy != "ours" && federationStrategy != "theirs" {
		return HandleErrorRespectJSON("invalid strategy %q: must be 'ours' or 'theirs'", federationStrategy)
	}

	var peers []string
	if federationPeer != "" {
		peers = []string{federationPeer}
	} else {
		remotes, err := ds.ListRemotes(ctx)
		if err != nil {
			return HandleErrorRespectJSON("failed to list peers: %v", err)
		}
		for _, r := range remotes {
			if r.Name != "origin" {
				peers = append(peers, r.Name)
			}
		}
	}

	if len(peers) == 0 {
		return HandleErrorRespectJSON("no federation peers configured (use 'bd federation add-peer' to add peers)")
	}

	// Sync with each peer
	var results []*storage.SyncResult
	// beads-o35h0: track whether ANY peer sync failed. Previously the loop
	// printed ✗ (human-only) and continued, then the function ended with an
	// unconditional `return nil` — so `bd federation sync` returned RC=0 even
	// when every peer merge failed. Federation sync is the multi-town data
	// path; a false-success exit code means automation (`bd federation sync &&
	// next` or `$?` checks) proceeds on a silent divergence. Accumulate the
	// failure and SilentExit(1) at the end if any peer failed.
	syncFailed := false
	for _, peer := range peers {
		if !jsonOutput {
			fmt.Printf("%s Syncing with %s...\n", ui.RenderAccent("🔄"), peer)
		}

		result, err := ds.Sync(ctx, peer, federationStrategy)
		results = append(results, result)

		if err != nil {
			syncFailed = true
			// beads-o35h0: surface the error in the JSON result too. Error is
			// `json:"-"` (an error type marshals to {}), so mirror it into the
			// string ErrorMsg field the human ✗ line already shows. result may
			// be non-nil on every failure path (both backends return the
			// populated result), but guard defensively.
			populateSyncResultErrorMsgs(result, err)
			if !jsonOutput {
				fmt.Printf("  %s %v\n", ui.RenderFail("✗"), err)
				// beads-sa08u: when the peer merge fails because the two towns
				// were independently init'd (diverged histories with no common
				// ancestor), a bare "merge failed: ..." line leaves the operator
				// with no way forward. `bd dolt push/pull` classify the SAME Dolt
				// error via isDivergedHistoryErr and print recovery guidance —
				// but their guidance (bd bootstrap / bd dolt push --force) is
				// local↔remote-origin specific and WRONG for a peer↔peer
				// federation divergence (neither town is the other's origin), so
				// emit federation-appropriate guidance instead. (Human-only; the
				// --json path already carries the error string via o35h0.)
				if isDivergedHistoryErr(err) {
					printFederationDivergedGuidance(peer)
				} else if isAncestorPKMismatchErr(err) {
					// beads-y19rc: sibling of the diverged-history leg above.
					// A PK-reshaping migration run independently on each town
					// (the #4259 fork) makes DOLT_MERGE refuse with "different
					// primary keys". bd dolt push/pull route this SAME error to
					// recovery guidance, but their bd-bootstrap / --force advice
					// is local↔remote-origin specific and wrong peer↔peer.
					printFederationPKMismatchGuidance(peer, err)
				}
			}
			continue
		}

		// beads-00oy4: mirror a NON-fatal push error into the JSON-visible
		// PushErrorMsg (see populateSyncResultErrorMsgs). No fatal err here, so
		// pass nil — this must NOT set syncFailed (a push failure is non-fatal;
		// RC stays 0), it only restores the --json signal.
		populateSyncResultErrorMsgs(result, nil)

		if !jsonOutput {
			if result.Fetched {
				fmt.Printf("  %s Fetched\n", ui.RenderPass("✓"))
			}
			if result.Merged {
				fmt.Printf("  %s Merged", ui.RenderPass("✓"))
				if result.PulledCommits > 0 {
					fmt.Printf(" (%d commits)", result.PulledCommits)
				}
				fmt.Println()
			}
			if len(result.Conflicts) > 0 {
				if result.ConflictsResolved {
					fmt.Printf("  %s Resolved %d conflicts using %s strategy\n",
						ui.RenderPass("✓"), len(result.Conflicts), federationStrategy)
				} else {
					fmt.Printf("  %s %d conflicts need resolution\n",
						ui.RenderWarn("⚠"), len(result.Conflicts))
					for _, c := range result.Conflicts {
						fmt.Printf("    - %s\n", c.Field)
					}
				}
			}
			if result.Pushed {
				fmt.Printf("  %s Pushed\n", ui.RenderPass("✓"))
			} else if result.PushError != nil {
				fmt.Printf("  %s Push skipped: %v\n", ui.RenderMuted("○"), result.PushError)
			}
		}
	}

	if jsonOutput {
		// beads-o35h0: include a top-level `failed` flag so a structured
		// consumer has an unambiguous per-invocation failure signal in addition
		// to each result's `error` string.
		if jerr := outputJSON(map[string]interface{}{
			"peers":   peers,
			"results": results,
			"failed":  syncFailed,
		}); jerr != nil {
			return jerr
		}
		if syncFailed {
			return SilentExit()
		}
		return nil
	}
	// beads-o35h0: non-zero exit when any peer sync failed (the per-peer ✗ line
	// was printed above; SilentExit avoids double-printing an error).
	if syncFailed {
		return SilentExit()
	}
	return nil
}

func runFederationStatus(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("federation-status")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	if err := requireDirectFederation("status"); err != nil {
		return err
	}

	ds, err := getFederatedStore()
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	allRemotes, err := ds.ListRemotes(ctx)
	if err != nil {
		return HandleErrorRespectJSON("failed to list remotes: %v", err)
	}
	remoteURLs := make(map[string]string)
	for _, r := range allRemotes {
		remoteURLs[r.Name] = r.URL
	}

	var peers []string
	if federationPeer != "" {
		peers = []string{federationPeer}
	} else {
		for _, r := range allRemotes {
			peers = append(peers, r.Name)
		}
	}

	if len(peers) == 0 {
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"peers":           []string{},
				"pending_changes": 0,
			})
		}
		fmt.Println("No federation peers configured.")
		return nil
	}

	doltStatus, _ := ds.Status(ctx)
	pendingChanges := 0
	if doltStatus != nil {
		pendingChanges = len(doltStatus.Staged) + len(doltStatus.Unstaged)
	}

	// beads-7mm8: json tags keep the --json output snake_case; without them
	// json.Marshal emits the PascalCase Go field names (Status/URL/...),
	// violating the snake_case JSON contract (same class as hooks-list kwyuug).
	type peerStatus struct {
		Status      *storage.SyncStatus `json:"status"`
		URL         string              `json:"url"`
		Reachable   bool                `json:"reachable"`
		ReachError  string              `json:"reach_error"`
		StatusError string              `json:"status_error"`
	}
	var peerStatuses []peerStatus

	for _, peer := range peers {
		ps := peerStatus{
			URL: remoteURLs[peer],
		}

		// beads-628e: don't discard the SyncStatus error and never store a nil
		// status — a backend may return (nil, err) (embeddeddolt did before the
		// parity fix), and the render loop below dereferences ps.Status. Keep a
		// non-nil placeholder so the peer still renders and surface the error.
		if status, err := ds.SyncStatus(ctx, peer); err == nil && status != nil {
			ps.Status = status
		} else {
			ps.Status = &storage.SyncStatus{Peer: peer, LocalAhead: -1, LocalBehind: -1}
			if err != nil {
				ps.StatusError = err.Error()
			}
		}

		fetchErr := ds.Fetch(ctx, peer)
		if fetchErr == nil {
			ps.Reachable = true
			if status, err := ds.SyncStatus(ctx, peer); err == nil && status != nil {
				ps.Status = status
			} else if err != nil {
				ps.StatusError = err.Error()
			}
		} else {
			ps.ReachError = fetchErr.Error()
		}

		peerStatuses = append(peerStatuses, ps)
	}

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"peers":           peerStatuses,
			"pending_changes": pendingChanges,
		})
	}

	fmt.Printf("\n%s Federation Status:\n\n", ui.RenderAccent("🌐"))

	if pendingChanges > 0 {
		fmt.Printf("  %s %d pending local changes\n\n", ui.RenderWarn("⚠"), pendingChanges)
	}

	for _, ps := range peerStatuses {
		status := ps.Status
		// beads-628e: defense-in-depth — never deref a nil status even if a
		// future backend returns one. The collection loop already substitutes a
		// placeholder, so this is belt-and-suspenders.
		if status == nil {
			status = &storage.SyncStatus{Peer: "(unknown)", LocalAhead: -1, LocalBehind: -1}
		}
		fmt.Printf("  %s  %s\n", ui.RenderAccent(status.Peer), ui.RenderMuted(ps.URL))

		if ps.Reachable {
			fmt.Printf("    %s Reachable\n", ui.RenderPass("✓"))
		} else {
			fmt.Printf("    %s Unreachable: %s\n", ui.RenderFail("✗"), ps.ReachError)
		}

		if ps.StatusError != "" {
			fmt.Printf("    %s Status unavailable: %s\n", ui.RenderWarn("⚠"), ps.StatusError)
		}

		if status.LocalAhead >= 0 {
			fmt.Printf("    Ahead:  %d commits\n", status.LocalAhead)
			fmt.Printf("    Behind: %d commits\n", status.LocalBehind)
		} else {
			fmt.Printf("    Sync:   %s\n", ui.RenderMuted("not fetched yet"))
		}

		if !status.LastSync.IsZero() {
			fmt.Printf("    Last sync: %s\n", status.LastSync.Format("2006-01-02 15:04:05"))
		}

		if status.HasConflicts {
			fmt.Printf("    %s Unresolved conflicts\n", ui.RenderWarn("⚠"))
		}
		fmt.Println()
	}
	return nil
}

func runFederationAddPeer(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("federation-add-peer")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	if err := requireDirectFederation("add-peer"); err != nil {
		return err
	}

	name := args[0]
	url := args[1]

	// beads-jkbyt: validate the peer name at the command layer so BOTH routes
	// are guarded — the AddFederationPeer branch (--user/--sovereignty) already
	// validates inside AddFederationPeerInTx, but the plain AddRemote branch
	// below does not. Without this, `add-peer origin <url>` would clobber the
	// backing "origin" Dolt remote that bd dolt push/pull target. ValidatePeerName
	// rejects the reserved name; AddRemote itself stays permissive because
	// legitimate origin setup uses it directly.
	if err := issueops.ValidatePeerName(name); err != nil {
		return HandleErrorRespectJSON("invalid peer name: %v", err)
	}

	password := federationPassword
	if federationUser != "" && password == "" {
		fmt.Fprint(os.Stderr, "Password: ")
		pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return HandleErrorRespectJSON("failed to read password: %v", err)
		}
		password = string(pwBytes)
	}

	sov := federationSov
	if sov != "" {
		sov = strings.ToUpper(sov)
		if sov != "T1" && sov != "T2" && sov != "T3" && sov != "T4" {
			return HandleErrorRespectJSON("invalid sovereignty tier: %s (must be T1, T2, T3, or T4)", federationSov)
		}
	}

	// A sovereignty tier is policy state persisted in the federation_peers row
	// (schema 0015: `sovereignty` column + index), independent of auth. Route
	// through AddFederationPeer whenever EITHER --user OR --sovereignty is set,
	// so the tier is actually persisted — the plain AddRemote path only creates
	// a Dolt remote and would silently drop the tier while the output below
	// echoes it back as stored (beads-pib1g). Symmetric-inverse of beads-af5te,
	// which routed remove-peer through the credential-aware method. An empty
	// username/password is tolerated (username is nullable; encryptPassword("")
	// returns nil), so a sovereignty-only peer stores cleanly.
	if federationUser != "" || sov != "" {
		peer := &storage.FederationPeer{
			Name:        name,
			RemoteURL:   url,
			Username:    federationUser,
			Password:    password,
			Sovereignty: sov,
		}
		if err := store.AddFederationPeer(ctx, peer); err != nil {
			return HandleErrorRespectJSON("failed to add peer: %v", err)
		}
	} else {
		if err := store.AddRemote(ctx, name, url); err != nil {
			return HandleErrorRespectJSON("failed to add peer: %v", err)
		}
	}

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"added":       name,
			"url":         url,
			"has_auth":    federationUser != "",
			"sovereignty": sov,
		})
	}

	fmt.Printf("Added peer %s: %s\n", ui.RenderAccent(name), url)
	if federationUser != "" {
		fmt.Printf("  User: %s (credentials stored)\n", federationUser)
	}
	if sov != "" {
		fmt.Printf("  Sovereignty: %s\n", sov)
	}
	return nil
}

func runFederationRemovePeer(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("federation-remove-peer")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	if err := requireDirectFederation("remove-peer"); err != nil {
		return err
	}

	name := args[0]

	// RemoveFederationPeer deletes the federation_peers credential row (added by
	// add-peer --user/--password) AND removes the underlying Dolt remote best-effort.
	// Calling RemoveRemote alone (beads-af5te) left the encrypted credential row
	// orphaned forever — invisible to list-peers/status, which read ListRemotes.
	if err := store.RemoveFederationPeer(ctx, name); err != nil {
		return HandleErrorRespectJSON("failed to remove peer: %v", err)
	}

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"removed": name,
		})
	}

	fmt.Printf("Removed peer: %s\n", name)
	return nil
}

func runFederationListPeers(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("federation-list-peers")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	if err := requireDirectFederation("list-peers"); err != nil {
		return err
	}

	remotes, err := store.ListRemotes(ctx)
	if err != nil {
		return HandleErrorRespectJSON("failed to list peers: %v", err)
	}

	if jsonOutput {
		return outputJSON(formatFederationPeerListJSON(remotes))
	}

	if len(remotes) == 0 {
		fmt.Println("No federation peers configured.")
		return nil
	}

	fmt.Printf("\n%s Federation Peers:\n\n", ui.RenderAccent("🌐"))
	for _, r := range remotes {
		fmt.Printf("  %s  %s\n", ui.RenderAccent(r.Name), ui.RenderMuted(r.URL))
	}
	fmt.Println()
	return nil
}

// federationPeerListJSON is a DELIBERATE compat shim (#4236): the PascalCase
// json tags "Name"/"URL" are intentionally preserved for existing consumers of
// `bd federation list-peers --json` and pinned by
// TestFormatFederationPeerListJSONPreservesLegacyKeys. beads-7mm8 asked to
// snake_case these too, but that would silently break the documented legacy
// contract — left unchanged; flagged on the bead for a PM call (a compat break
// needs a deliberate deprecation, not a drive-by rename).
type federationPeerListJSON struct {
	Name string `json:"Name"`
	URL  string `json:"URL"`
}

func formatFederationPeerListJSON(remotes []storage.RemoteInfo) []federationPeerListJSON {
	out := make([]federationPeerListJSON, 0, len(remotes))
	for _, r := range remotes {
		out = append(out, federationPeerListJSON{
			Name: r.Name,
			URL:  r.URL,
		})
	}
	return out
}
