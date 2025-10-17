#!/bin/bash
# Delete all test issues (open and closed) from database

set -e

echo "Deleting all test issues..."
echo ""

# Get all test issues from database (open and closed)
test_ids=$(./bd list --json --no-auto-import | jq -r '.[] | select(.title | test("^(parallel_test|race_test|stress_test|final_test|final_review_test|verification_)")) | .id')

count=$(echo "$test_ids" | wc -l | tr -d ' ')
echo "Found $count test issues to delete"
echo ""

# Delete each one
i=0
for id in $test_ids; do
    i=$((i+1))
    if [ $((i % 25)) -eq 0 ]; then
        echo "  Progress: $i/$count"
    fi
    ./bd delete "$id" --force --no-auto-import 2>&1 | grep -E "Error" || true
done

echo ""
echo "Done! Deleted $count test issues."
echo ""
./bd stats --no-auto-import
