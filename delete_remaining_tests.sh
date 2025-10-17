#!/bin/bash
# Delete remaining test issues from current database

set -e

echo "Deleting remaining test issues..."
echo ""

# Get current test issues from database
test_ids=$(./bd list --status open --json --no-auto-import | jq -r '.[] | select(.title | test("^(parallel_test|race_test|stress_test|final_test|final_review_test|verification_)")) | .id')

count=$(echo "$test_ids" | wc -l | tr -d ' ')
echo "Found $count test issues to delete"
echo ""

# Delete each one
for id in $test_ids; do
    ./bd delete "$id" --force --no-auto-import 2>&1 | grep -E "(✓|Error)" || true
done

echo ""
echo "Done! Deleted test issues."
echo ""
./bd stats --no-auto-import
