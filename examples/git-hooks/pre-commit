#!/usr/bin/env bash
#
# Beads pre-commit hook
# Automatically exports SQLite database to JSONL before committing
#
# Install: cp examples/git-hooks/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit

set -e

# Check if bd is installed
if ! command -v bd &> /dev/null; then
    echo "Warning: bd not found in PATH, skipping export"
    exit 0
fi

# Check if .beads directory exists
if [[ ! -d .beads ]]; then
    # No beads database, nothing to do
    exit 0
fi

# Export issues to JSONL
echo "🔗 Exporting beads issues to JSONL..."

if bd export --format=jsonl -o .beads/issues.jsonl 2>/dev/null; then
    # Add the JSONL file to the commit
    git add .beads/issues.jsonl
    echo "✓ Beads issues exported and staged"
else
    echo "Warning: bd export failed, continuing anyway"
fi

exit 0
