package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/storage/sqlite"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize bd in the current directory",
	Long: `Initialize bd in the current directory by creating a .beads/ directory
and database file. Optionally specify a custom issue prefix.`,
	Run: func(cmd *cobra.Command, args []string) {
		prefix, _ := cmd.Flags().GetString("prefix")
		quiet, _ := cmd.Flags().GetBool("quiet")

		// Check BEADS_DB environment variable if --db flag not set
		// (PersistentPreRun doesn't run for init command)
		if dbPath == "" {
			if envDB := os.Getenv("BEADS_DB"); envDB != "" {
				dbPath = envDB
			}
		}

		if prefix == "" {
			// Auto-detect from directory name
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to get current directory: %v\n", err)
				os.Exit(1)
			}
			prefix = filepath.Base(cwd)
		}

		// Normalize prefix: strip trailing hyphens
		// The hyphen is added automatically during ID generation
		prefix = strings.TrimRight(prefix, "-")

		// Create database
		// Use global dbPath if set via --db flag or BEADS_DB env var, otherwise default to .beads/{prefix}.db
		initDBPath := dbPath
		if initDBPath == "" {
		 initDBPath = filepath.Join(".beads", prefix+".db")
		}
		
		// Determine if we should create .beads/ directory in CWD
		// Only create it if the database will be stored there
	cwd, err := os.Getwd()
		if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get current directory: %v\n", err)
		os.Exit(1)
	}
	
	localBeadsDir := filepath.Join(cwd, ".beads")
	initDBDir := filepath.Dir(initDBPath)
	
	// Convert both to absolute paths for comparison
	localBeadsDirAbs, err := filepath.Abs(localBeadsDir)
	if err != nil {
		localBeadsDirAbs = filepath.Clean(localBeadsDir)
	}
	initDBDirAbs, err := filepath.Abs(initDBDir)
	if err != nil {
		initDBDirAbs = filepath.Clean(initDBDir)
	}
	
	useLocalBeads := filepath.Clean(initDBDirAbs) == filepath.Clean(localBeadsDirAbs)
	
	if useLocalBeads {
		// Create .beads directory
		if err := os.MkdirAll(localBeadsDir, 0750); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to create .beads directory: %v\n", err)
			os.Exit(1)
		}

		// Create .gitignore in .beads directory
		gitignorePath := filepath.Join(localBeadsDir, ".gitignore")
		gitignoreContent := `# SQLite databases
*.db
*.db-journal
*.db-wal
*.db-shm

# Daemon runtime files
daemon.log
daemon.pid
bd.sock

# Legacy database files
db.sqlite
bd.db

# Keep JSONL exports (source of truth for git)
!*.jsonl
`
			if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create .gitignore: %v\n", err)
				// Non-fatal - continue anyway
			}
		}
	
		// Ensure parent directory exists for the database
		if err := os.MkdirAll(initDBDir, 0750); err != nil {
		 fmt.Fprintf(os.Stderr, "Error: failed to create database directory %s: %v\n", initDBDir, err)
		 os.Exit(1)
		}
		
		store, err := sqlite.New(initDBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to create database: %v\n", err)
			os.Exit(1)
		}

		// Set the issue prefix in config
		ctx := context.Background()
		if err := store.SetConfig(ctx, "issue_prefix", prefix); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to set issue prefix: %v\n", err)
			_ = store.Close()
			os.Exit(1)
		}

		// Store the bd version in metadata (for version mismatch detection)
		if err := store.SetMetadata(ctx, "bd_version", Version); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to store version metadata: %v\n", err)
		// Non-fatal - continue anyway
		}

		// Check if git has existing issues to import (fresh clone scenario)
		issueCount, jsonlPath := checkGitForIssues()
		if issueCount > 0 {
		if !quiet {
		fmt.Fprintf(os.Stderr, "\n✓ Database initialized. Found %d issues in git, importing...\n", issueCount)
		}
		
		if err := importFromGit(ctx, initDBPath, store, jsonlPath); err != nil {
		if !quiet {
		fmt.Fprintf(os.Stderr, "Warning: auto-import failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "Try manually: git show HEAD:%s | bd import -i /dev/stdin\n", jsonlPath)
		}
		// Non-fatal - continue with empty database
		} else {
		// CRITICAL: Immediately export to local JSONL to prevent daemon race condition
		// The daemon might auto-start before the 5-second auto-flush debounce completes
		// Write to exact git-relative path to prevent path drift
		gitRoot := findGitRoot()
		if gitRoot == "" {
		if !quiet {
		 fmt.Fprintf(os.Stderr, "Warning: could not find git root for export\n")
		 }
		} else {
		absJSONL := filepath.Join(gitRoot, filepath.FromSlash(jsonlPath))
		 if err := os.MkdirAll(filepath.Dir(absJSONL), 0750); err != nil {
		   if !quiet {
					fmt.Fprintf(os.Stderr, "Warning: failed to create export dir: %v\n", err)
				}
			} else if err := exportToJSONLWithStore(ctx, store, absJSONL); err != nil {
				if !quiet {
					fmt.Fprintf(os.Stderr, "Warning: failed to export after import: %v\n", err)
				}
			}
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "✓ Successfully imported %d issues from git.\n\n", issueCount)
		}
	}
}

	// Safety check: verify import succeeded and catch silent data loss
	stats, err := store.GetStatistics(ctx)
	if err == nil && stats.TotalIssues == 0 {
		// DB empty after init - check if git has issues we failed to import
		recheck, recheckPath := checkGitForIssues()
		if recheck > 0 {
			fmt.Fprintf(os.Stderr, "\n❌ ERROR: Database empty but git has %d issues!\n", recheck)
			fmt.Fprintf(os.Stderr, "Auto-import failed. Manual recovery:\n")
			fmt.Fprintf(os.Stderr, "  git show HEAD:%s | bd import -i /dev/stdin\n", filepath.ToSlash(recheckPath))
			// Only suggest local file import if file exists
			gitRoot := findGitRoot()
			if gitRoot != "" {
				localFile := filepath.Join(gitRoot, filepath.FromSlash(recheckPath))
				if _, err := os.Stat(localFile); err == nil {
					fmt.Fprintf(os.Stderr, "Or:\n")
					fmt.Fprintf(os.Stderr, "  bd import -i %s\n", localFile)
				}
			}
			_ = store.Close()
			os.Exit(1)
		}
	}

if err := store.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to close database: %v\n", err)
}

// Skip output if quiet mode
if quiet {
		return
}

		green := color.New(color.FgGreen).SprintFunc()
		cyan := color.New(color.FgCyan).SprintFunc()

		fmt.Printf("\n%s bd initialized successfully!\n\n", green("✓"))
		fmt.Printf("  Database: %s\n", cyan(initDBPath))
		fmt.Printf("  Issue prefix: %s\n", cyan(prefix))
		fmt.Printf("  Issues will be named: %s\n\n", cyan(prefix+"-1, "+prefix+"-2, ..."))
		fmt.Printf("Run %s to get started.\n\n", cyan("bd quickstart"))
	},
}

func init() {
	initCmd.Flags().StringP("prefix", "p", "", "Issue prefix (default: current directory name)")
	initCmd.Flags().BoolP("quiet", "q", false, "Suppress output (quiet mode)")
	rootCmd.AddCommand(initCmd)
}
