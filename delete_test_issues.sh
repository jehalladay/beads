#!/bin/bash
# Delete test issues - parallel_test, race_test, stress_test, final_test, final_review_test

set -e

echo "Deleting test issues..."
echo ""

# Get the list of test issue IDs (excluding closed)
test_ids=$(grep -E "^## (parallel_test|race_test|stress_test|final_test|final_review_test)" DUPLICATES_REPORT.md -A 20 | grep "^- bd-" | grep -v "closed" | awk '{print $2}' | sort -u)

count=$(echo "$test_ids" | wc -l)
echo "Found $count test issues to delete"
echo ""

# Delete each one (disable auto-import to avoid conflicts)
for id in $test_ids; do
    echo "Deleting $id..."
    ./bd delete "$id" --force --no-auto-import
done

echo ""
echo "Done! Deleted $count test issues."
echo ""
./bd stats
