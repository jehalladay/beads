#!/bin/bash
# Analyze duplicate issues

echo "# Duplicate Issues Report"
echo ""
echo "Generated: $(date)"
echo ""

./bd list --json | jq -r 'group_by(.title) | .[] | select(length > 1) | {
  title: .[0].title,
  count: length,
  issues: [.[] | {id, status, created_at}]
} | "## \(.title)\nCount: \(.count)\n" + (.issues | map("- \(.id) (\(.status)) created \(.created_at)") | join("\n")) + "\n"'
