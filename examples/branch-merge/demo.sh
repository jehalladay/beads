#!/bin/bash
# Demo script for branch merge collision resolution workflow
# This script simulates a branch merge with ID collisions

set -e  # Exit on error

echo "=== Branch Merge Collision Resolution Demo ==="
echo ""

# Check if bd is available
if ! command -v bd &> /dev/null; then
    echo "Error: bd command not found. Please install bd first."
    echo "Run: go install github.com/steveyegge/beads/cmd/bd@latest"
    exit 1
fi

# Create a temporary directory for the demo
DEMO_DIR=$(mktemp -d -t bd-merge-demo-XXXXXX)
echo "Demo directory: $DEMO_DIR"
cd "$DEMO_DIR"

# Initialize git repo
echo ""
echo "Step 1: Initialize git repo and bd database"
git init
git config user.name "Demo User"
git config user.email "demo@example.com"
bd init --prefix demo

# Create initial commit
echo "Initial project" > README.txt
git add README.txt .beads/
git commit -m "Initial commit"

# Create issues on main branch
echo ""
echo "Step 2: Create issues on main branch"
bd create "Implement login" -d "User authentication system" -t feature -p 1 --json
bd create "Fix memory leak" -d "Memory leak in parser" -t bug -p 0 --json
bd create "Update docs" -d "Document new API" -t task -p 2 --json

echo ""
echo "Main branch issues:"
bd list

# Export and commit
bd export -o .beads/issues.jsonl
git add .beads/issues.jsonl
git commit -m "Add main branch issues (bd-1, bd-2, bd-3)"

# Create feature branch from earlier point
echo ""
echo "Step 3: Create feature branch"
git checkout -b feature-branch HEAD~1

# Reimport to get clean state
bd import -i .beads/issues.jsonl

# Create overlapping issues on feature branch
echo ""
echo "Step 4: Create different issues with same IDs on feature branch"
bd create "Add dashboard" -d "Admin dashboard feature" -t feature -p 2 --json
bd create "Improve performance" -d "Optimize queries" -t task -p 1 --json
bd create "Add metrics" -d "Monitoring and metrics" -t feature -p 1 --json

echo ""
echo "Feature branch issues:"
bd list

# Export and commit
bd export -o .beads/issues.jsonl
git add .beads/issues.jsonl
git commit -m "Add feature branch issues (bd-1, bd-2, bd-3)"

# Merge back to main
echo ""
echo "Step 5: Merge feature branch into main"
git checkout main

# Attempt merge (will conflict)
if git merge feature-branch --no-edit; then
    echo "Merge succeeded without conflicts"
else
    echo "Merge conflict detected - resolving..."
    # Keep both versions by accepting both sides
    # In a real scenario, you'd resolve this more carefully
    git checkout --ours .beads/issues.jsonl
    git checkout --theirs .beads/issues.jsonl --patch || true
    # For demo purposes, accept theirs
    git checkout --theirs .beads/issues.jsonl
    git add .beads/issues.jsonl
    git commit -m "Merge feature-branch"
fi

# Detect collisions
echo ""
echo "Step 6: Detect ID collisions"
echo "Running: bd import -i .beads/issues.jsonl --dry-run"
echo ""

if bd import -i .beads/issues.jsonl --dry-run; then
    echo "No collisions detected!"
else
    echo ""
    echo "Collisions detected (expected)!"
fi

# Resolve collisions
echo ""
echo "Step 7: Resolve collisions automatically"
echo "Running: bd import -i .beads/issues.jsonl --resolve-collisions"
echo ""

bd import -i .beads/issues.jsonl --resolve-collisions

# Show final state
echo ""
echo "Step 8: Final issue list after resolution"
bd list

# Show remapping details
echo ""
echo "Step 9: Show how dependencies and references are maintained"
echo "All text references like 'see bd-1' and dependencies were automatically updated!"

# Export final state
bd export -o .beads/issues.jsonl
git add .beads/issues.jsonl
git commit -m "Resolve collisions and finalize merge"

echo ""
echo "=== Demo Complete ==="
echo ""
echo "Summary:"
echo "- Created issues on main branch (bd-1, bd-2, bd-3)"
echo "- Created different issues on feature branch (also bd-1, bd-2, bd-3)"
echo "- Merged branches with Git"
echo "- Detected collisions with --dry-run"
echo "- Resolved collisions with --resolve-collisions"
echo "- Feature branch issues were renumbered to avoid conflicts"
echo ""
echo "Demo directory: $DEMO_DIR"
echo "You can explore the git history: cd $DEMO_DIR && git log --oneline"
echo ""
echo "To clean up: rm -rf $DEMO_DIR"
