#!/bin/bash
# Simple latency benchmark for bd-htfk
set -e

echo "# Latency Benchmark Results"
echo ""
echo "## Git Sync Latency Test (10 runs)"
echo ""

# Test git sync latency
for i in {1..10}; do
    start=$(date +%s%N)
    
    # Create, update, and sync an issue
    test_id=$(bd create "Latency test $i" -p 3 --json 2>/dev/null | jq -r '.id')
    bd update "$test_id" --status in_progress >/dev/null 2>&1
    bd sync >/dev/null 2>&1
    
    end=$(date +%s%N)
    latency_ms=$(((end - start) / 1000000))
    
    echo "Run $i: ${latency_ms}ms"
    
    # Cleanup
    bd close "$test_id" --reason "test" >/dev/null 2>&1
done

echo ""
echo "## Notes"
echo "- Git sync includes: create → update → export → commit → push → pull → import"
echo "- This represents the full round-trip time for issue changes to sync via git"
echo "- Agent Mail latency test skipped (server not running)"
echo "- Expected git latency: 1000-5000ms"
echo "- Expected Agent Mail latency: <100ms (when server running)"
