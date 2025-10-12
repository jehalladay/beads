#!/usr/bin/env bash
#
# Install Beads git hooks
#
# This script copies the hooks to .git/hooks/ and makes them executable

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOKS_DIR="$(git rev-parse --git-dir)/hooks"

# Check if we're in a git repository
if ! git rev-parse --git-dir &> /dev/null; then
    echo "Error: Not in a git repository"
    exit 1
fi

echo "Installing Beads git hooks to $HOOKS_DIR"
echo ""

# Install pre-commit hook
if [[ -f "$HOOKS_DIR/pre-commit" ]]; then
    echo "⚠ $HOOKS_DIR/pre-commit already exists"
    read -p "Overwrite? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Skipping pre-commit"
    else
        cp "$SCRIPT_DIR/pre-commit" "$HOOKS_DIR/pre-commit"
        chmod +x "$HOOKS_DIR/pre-commit"
        echo "✓ Installed pre-commit hook"
    fi
else
    cp "$SCRIPT_DIR/pre-commit" "$HOOKS_DIR/pre-commit"
    chmod +x "$HOOKS_DIR/pre-commit"
    echo "✓ Installed pre-commit hook"
fi

# Install post-merge hook
if [[ -f "$HOOKS_DIR/post-merge" ]]; then
    echo "⚠ $HOOKS_DIR/post-merge already exists"
    read -p "Overwrite? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Skipping post-merge"
    else
        cp "$SCRIPT_DIR/post-merge" "$HOOKS_DIR/post-merge"
        chmod +x "$HOOKS_DIR/post-merge"
        echo "✓ Installed post-merge hook"
    fi
else
    cp "$SCRIPT_DIR/post-merge" "$HOOKS_DIR/post-merge"
    chmod +x "$HOOKS_DIR/post-merge"
    echo "✓ Installed post-merge hook"
fi

# Install post-checkout hook
if [[ -f "$HOOKS_DIR/post-checkout" ]]; then
    echo "⚠ $HOOKS_DIR/post-checkout already exists"
    read -p "Overwrite? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Skipping post-checkout"
    else
        cp "$SCRIPT_DIR/post-checkout" "$HOOKS_DIR/post-checkout"
        chmod +x "$HOOKS_DIR/post-checkout"
        echo "✓ Installed post-checkout hook"
    fi
else
    cp "$SCRIPT_DIR/post-checkout" "$HOOKS_DIR/post-checkout"
    chmod +x "$HOOKS_DIR/post-checkout"
    echo "✓ Installed post-checkout hook"
fi

echo ""
echo "✓ Beads git hooks installed successfully!"
echo ""
echo "These hooks will:"
echo "  • Export issues to JSONL before every commit"
echo "  • Import issues from JSONL after merges"
echo "  • Import issues from JSONL after branch checkouts"
echo ""
echo "To uninstall, simply delete the hooks from $HOOKS_DIR"
