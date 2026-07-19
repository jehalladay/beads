package main

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// printTruncationHint emits a one-line notice to stderr when the list output
// was truncated by --limit, so users and agents can't mistake a partial view
// for a complete one (GH#3212, GH#788).
func printTruncationHint(truncated bool, effectiveLimit int) {
	if !truncated || effectiveLimit <= 0 || !ui.IsStderrTerminal() {
		return
	}
	msg := fmt.Sprintf("\nShowing %d issues; more results matched but were hidden by --limit. Use --limit 0 for all, or --limit N to raise the cap.\n", effectiveLimit)
	fmt.Fprint(os.Stderr, ui.RenderWarn(msg))
}

// printTreeLimitIgnoredHint warns when `bd list --parent X --limit N --pretty`
// was given a positive --limit that the child count exceeds. The tree view
// intentionally renders the FULL subtree (truncating mid-hierarchy would orphan
// children), so --limit is not applied here — but silently ignoring it is a
// parity gap vs the --json/compact paths. This makes the ignore explicit
// (beads-3dr5). Stderr + terminal-gated, matching printTruncationHint.
func printTreeLimitIgnoredHint(effectiveLimit, childCount int) {
	if !treeLimitIgnored(effectiveLimit, childCount) || !ui.IsStderrTerminal() {
		return
	}
	msg := fmt.Sprintf("\n--limit %d ignored in --parent tree view (%d children shown; truncating a tree would orphan descendants). Use --flat or --json to page.\n", effectiveLimit, childCount)
	fmt.Fprint(os.Stderr, ui.RenderWarn(msg))
}

// treeLimitIgnored reports whether a positive --limit was effectively ignored
// by the --parent tree render (child count exceeds it). Split from the printer
// so the decision is unit-testable without a TTY (the printer's stderr write is
// terminal-gated, matching printTruncationHint) (beads-3dr5).
func treeLimitIgnored(effectiveLimit, childCount int) bool {
	return effectiveLimit > 0 && childCount > effectiveLimit
}

func outputDotFormat(issues []*types.Issue, depsByIssueID map[string][]*types.Dependency) error {
	fmt.Println("digraph dependencies {")
	fmt.Println("  rankdir=TB;")
	fmt.Println("  node [shape=box, style=rounded];")
	fmt.Println()

	// Build map of all issues for quick lookup
	issueMap := make(map[string]*types.Issue)
	for _, issue := range issues {
		issueMap[issue.ID] = issue
	}

	// Output nodes with labels including ID, type, priority, and status
	for _, issue := range issues {
		// Build label with ID, type, priority, and title (using actual newlines)
		label := fmt.Sprintf("%s\n[%s P%d]\n%s\n(%s)",
			issue.ID,
			issue.IssueType,
			issue.Priority,
			issue.Title,
			issue.Status)

		// Color by status only - keep it simple
		fillColor := "white"
		fontColor := "black"

		switch issue.Status {
		case "closed":
			fillColor = "lightgray"
			fontColor = "dimgray"
		case "in_progress":
			fillColor = "lightyellow"
		case "blocked":
			fillColor = "lightcoral"
		}

		fmt.Printf("  %q [label=%q, style=\"rounded,filled\", fillcolor=%q, fontcolor=%q];\n",
			issue.ID, label, fillColor, fontColor)
	}
	fmt.Println()

	// Output edges with labels for dependency type
	for _, issue := range issues {
		for _, dep := range depsByIssueID[issue.ID] {
			// Only output edges where both nodes are in the filtered list
			if issueMap[dep.DependsOnID] != nil {
				// Color code by dependency type
				color := "black"
				style := "solid"
				switch dep.Type {
				case "blocks":
					color = "red"
					style = "bold"
				case "parent-child":
					color = "blue"
				case "discovered-from":
					color = "green"
					style = "dashed"
				case "related":
					color = "gray"
					style = "dashed"
				}
				fmt.Printf("  %q -> %q [label=%q, color=%s, style=%s];\n",
					issue.ID, dep.DependsOnID, dep.Type, color, style)
			}
		}
	}

	fmt.Println("}")
	return nil
}

func outputFormattedList(issues []*types.Issue, depsByIssueID map[string][]*types.Dependency, formatStr string) error {
	// Handle special 'dot' format (Graphviz output)
	if formatStr == "dot" {
		return outputDotFormat(issues, depsByIssueID)
	}

	// 'digraph' is a graph-EDGE preset (one line per dependency edge), whose
	// template fields are edge-level (.IssueID/.DependsOnID/.Type). Any other
	// --format value is a user Go template rendered PER ISSUE against the issue
	// struct, so .ID/.Title/.IssueType/etc. resolve (beads-ibud: previously ALL
	// --format templates ran the per-edge path, so a documented per-issue
	// template like '{{.ID}}' saw only edge keys → "<no value>" for every field,
	// and issues with no deps produced no output at all).
	if formatStr == "digraph" {
		return outputEdgeTemplate(issues, depsByIssueID, "{{.IssueID}} {{.DependsOnID}}")
	}
	return outputIssueTemplate(issues, formatStr)
}

// outputIssueTemplate renders a user-supplied Go template once per issue with
// the issue struct as data, so exported fields (.ID, .Title, .IssueType,
// .Priority, .Status, .Assignee, .Description, ...) resolve by name (beads-ibud).
func outputIssueTemplate(issues []*types.Issue, formatStr string) error {
	tmpl, err := template.New("format").Parse(formatStr)
	if err != nil {
		return fmt.Errorf("invalid format template: %w", err)
	}
	for _, issue := range issues {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, issue); err != nil {
			return fmt.Errorf("template execution error: %w", err)
		}
		fmt.Println(buf.String())
	}
	return nil
}

// outputEdgeTemplate renders one line per in-filter dependency edge, exposing
// edge-level fields (.IssueID/.DependsOnID/.Type) plus the full .Issue and
// .Dependency for the digraph preset and edge-oriented custom formats.
func outputEdgeTemplate(issues []*types.Issue, depsByIssueID map[string][]*types.Dependency, templateStr string) error {
	tmpl, err := template.New("format").Parse(templateStr)
	if err != nil {
		return fmt.Errorf("invalid format template: %w", err)
	}

	// Build map of all issues for quick lookup
	issueMap := make(map[string]bool)
	for _, issue := range issues {
		issueMap[issue.ID] = true
	}

	// For each issue, output its dependencies using the template
	for _, issue := range issues {
		for _, dep := range depsByIssueID[issue.ID] {
			// Only output edges where both nodes are in the filtered list
			if issueMap[dep.DependsOnID] {
				// Template data includes both issue and dependency info
				data := map[string]interface{}{
					"IssueID":     issue.ID,
					"DependsOnID": dep.DependsOnID,
					"Type":        dep.Type,
					"Issue":       issue,
					"Dependency":  dep,
				}

				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, data); err != nil {
					return fmt.Errorf("template execution error: %w", err)
				}
				fmt.Println(buf.String())
			}
		}
	}

	return nil
}
