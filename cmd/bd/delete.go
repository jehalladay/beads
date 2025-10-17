package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/types"
)

var deleteCmd = &cobra.Command{
	Use:   "delete <issue-id>",
	Short: "Delete an issue and clean up references",
	Long: `Delete an issue and clean up all references to it.

This command will:
1. Remove all dependency links (any type, both directions) involving the issue
2. Update text references to "[deleted:ID]" in directly connected issues
3. Delete the issue from the database

This is a destructive operation that cannot be undone. Use with caution.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		issueID := args[0]
		force, _ := cmd.Flags().GetBool("force")
		
		ctx := context.Background()
		
		// Get the issue to be deleted
		issue, err := store.GetIssue(ctx, issueID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if issue == nil {
			fmt.Fprintf(os.Stderr, "Error: issue %s not found\n", issueID)
			os.Exit(1)
		}
		
		// Find all connected issues (dependencies in both directions)
		connectedIssues := make(map[string]*types.Issue)
		
		// Get dependencies (issues this one depends on)
		deps, err := store.GetDependencies(ctx, issueID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting dependencies: %v\n", err)
			os.Exit(1)
		}
		for _, dep := range deps {
			connectedIssues[dep.ID] = dep
		}
		
		// Get dependents (issues that depend on this one)
		dependents, err := store.GetDependents(ctx, issueID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting dependents: %v\n", err)
			os.Exit(1)
		}
		for _, dependent := range dependents {
			connectedIssues[dependent.ID] = dependent
		}
		
		// Get dependency records (outgoing) to count how many we'll remove
		depRecords, err := store.GetDependencyRecords(ctx, issueID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting dependency records: %v\n", err)
			os.Exit(1)
		}
		
		// Build the regex pattern for matching issue IDs (handles hyphenated IDs properly)
		// Pattern: (^|non-word-char)(issueID)($|non-word-char) where word-char includes hyphen
		idPattern := `(^|[^A-Za-z0-9_-])(` + regexp.QuoteMeta(issueID) + `)($|[^A-Za-z0-9_-])`
		re := regexp.MustCompile(idPattern)
		replacementText := `$1[deleted:` + issueID + `]$3`
		
		// Preview mode
		if !force {
			red := color.New(color.FgRed).SprintFunc()
			yellow := color.New(color.FgYellow).SprintFunc()
			
			fmt.Printf("\n%s\n", red("⚠️  DELETE PREVIEW"))
			fmt.Printf("\nIssue to delete:\n")
			fmt.Printf("  %s: %s\n", issueID, issue.Title)
			
			totalDeps := len(depRecords) + len(dependents)
			if totalDeps > 0 {
				fmt.Printf("\nDependency links to remove: %d\n", totalDeps)
				for _, dep := range depRecords {
					fmt.Printf("  %s → %s (%s)\n", dep.IssueID, dep.DependsOnID, dep.Type)
				}
				for _, dep := range dependents {
					fmt.Printf("  %s → %s (inbound)\n", dep.ID, issueID)
				}
			}
			
			if len(connectedIssues) > 0 {
				fmt.Printf("\nConnected issues where text references will be updated:\n")
				issuesWithRefs := 0
				for id, connIssue := range connectedIssues {
					// Check if there are actually text references using the fixed regex
					hasRefs := re.MatchString(connIssue.Description) ||
						(connIssue.Notes != "" && re.MatchString(connIssue.Notes)) ||
						(connIssue.Design != "" && re.MatchString(connIssue.Design)) ||
						(connIssue.AcceptanceCriteria != "" && re.MatchString(connIssue.AcceptanceCriteria))
					
					if hasRefs {
						fmt.Printf("  %s: %s\n", id, connIssue.Title)
						issuesWithRefs++
					}
				}
				if issuesWithRefs == 0 {
					fmt.Printf("  (none have text references)\n")
				}
			}
			
			fmt.Printf("\n%s\n", yellow("This operation cannot be undone!"))
			fmt.Printf("To proceed, run: %s\n\n", yellow("bd delete "+issueID+" --force"))
			return
		}
		
		// Actually delete
		
		// 1. Update text references in connected issues (all text fields)
		updatedIssueCount := 0
		for id, connIssue := range connectedIssues {
			updates := make(map[string]interface{})
			
			// Replace in description
			if re.MatchString(connIssue.Description) {
				newDesc := re.ReplaceAllString(connIssue.Description, replacementText)
				updates["description"] = newDesc
			}
			
			// Replace in notes
			if connIssue.Notes != "" && re.MatchString(connIssue.Notes) {
				newNotes := re.ReplaceAllString(connIssue.Notes, replacementText)
				updates["notes"] = newNotes
			}
			
			// Replace in design
			if connIssue.Design != "" && re.MatchString(connIssue.Design) {
				newDesign := re.ReplaceAllString(connIssue.Design, replacementText)
				updates["design"] = newDesign
			}
			
			// Replace in acceptance_criteria
			if connIssue.AcceptanceCriteria != "" && re.MatchString(connIssue.AcceptanceCriteria) {
				newAC := re.ReplaceAllString(connIssue.AcceptanceCriteria, replacementText)
				updates["acceptance_criteria"] = newAC
			}
			
			if len(updates) > 0 {
				if err := store.UpdateIssue(ctx, id, updates, actor); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: Failed to update references in %s: %v\n", id, err)
				} else {
					updatedIssueCount++
				}
			}
		}
		
		// 2. Remove all dependency links (outgoing)
		outgoingRemoved := 0
		for _, dep := range depRecords {
			if err := store.RemoveDependency(ctx, dep.IssueID, dep.DependsOnID, actor); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to remove dependency %s → %s: %v\n", 
					dep.IssueID, dep.DependsOnID, err)
			} else {
				outgoingRemoved++
			}
		}
		
		// 3. Remove inbound dependency links (issues that depend on this one)
		inboundRemoved := 0
		for _, dep := range dependents {
			if err := store.RemoveDependency(ctx, dep.ID, issueID, actor); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to remove dependency %s → %s: %v\n", 
					dep.ID, issueID, err)
			} else {
				inboundRemoved++
			}
		}
		
		// 4. Delete the issue itself from database
		if err := deleteIssue(ctx, issueID); err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting issue: %v\n", err)
			os.Exit(1)
		}
		
		// 5. Remove from JSONL (auto-flush can't see deletions)
		if err := removeIssueFromJSONL(issueID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to remove from JSONL: %v\n", err)
		}
		
		// Schedule auto-flush to update neighbors
		markDirtyAndScheduleFlush()
		
		totalDepsRemoved := outgoingRemoved + inboundRemoved
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"deleted":              issueID,
				"dependencies_removed": totalDepsRemoved,
				"references_updated":   updatedIssueCount,
			})
		} else {
			green := color.New(color.FgGreen).SprintFunc()
			fmt.Printf("%s Deleted %s\n", green("✓"), issueID)
			fmt.Printf("  Removed %d dependency link(s)\n", totalDepsRemoved)
			fmt.Printf("  Updated text references in %d issue(s)\n", updatedIssueCount)
		}
	},
}

// deleteIssue removes an issue from the database
// Note: This is a direct database operation since Storage interface doesn't have Delete
func deleteIssue(ctx context.Context, issueID string) error {
	// We need to access the SQLite storage directly
	// Check if store is SQLite storage
	type deleter interface {
		DeleteIssue(ctx context.Context, id string) error
	}
	
	if d, ok := store.(deleter); ok {
		return d.DeleteIssue(ctx, issueID)
	}
	
	return fmt.Errorf("delete operation not supported by this storage backend")
}

// removeIssueFromJSONL removes a deleted issue from the JSONL file
// Auto-flush cannot see deletions because the dirty_issues row is deleted with the issue
func removeIssueFromJSONL(issueID string) error {
	path := findJSONLPath()
	if path == "" {
		return nil // No JSONL file yet
	}
	
	// Read all issues except the deleted one
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No file, nothing to clean
		}
		return fmt.Errorf("failed to open JSONL: %w", err)
	}
	defer f.Close()
	
	var issues []*types.Issue
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var iss types.Issue
		if err := json.Unmarshal([]byte(line), &iss); err != nil {
			// Skip malformed lines
			continue
		}
		if iss.ID != issueID {
			issues = append(issues, &iss)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read JSONL: %w", err)
	}
	
	// Write to temp file atomically
	temp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	out, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	
	enc := json.NewEncoder(out)
	for _, iss := range issues {
		if err := enc.Encode(iss); err != nil {
			out.Close()
			os.Remove(temp)
			return fmt.Errorf("failed to write issue: %w", err)
		}
	}
	
	if err := out.Close(); err != nil {
		os.Remove(temp)
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	
	// Atomic rename
	if err := os.Rename(temp, path); err != nil {
		os.Remove(temp)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	
	return nil
}

func init() {
	deleteCmd.Flags().BoolP("force", "f", false, "Actually delete (without this flag, shows preview)")
	rootCmd.AddCommand(deleteCmd)
}
