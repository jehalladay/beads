#!/bin/bash
# Benchmark Agent Mail vs Git Sync latency
# Part of bd-htfk investigation

set -e

RESULTS_FILE="latency_benchmark_results.md"
AGENT_MAIL_URL="http://127.0.0.1:8765"
BEARER_TOKEN=$(grep BEARER_TOKEN ~/src/mcp_agent_mail/.env | cut -d= -f2 | tr -d '"')

echo "# Latency Benchmark: Agent Mail vs Git Sync" > "$RESULTS_FILE"
echo "" >> "$RESULTS_FILE"
echo "Date: $(date)" >> "$RESULTS_FILE"
echo "" >> "$RESULTS_FILE"

# Function to measure git sync latency
measure_git_sync() {
    local iterations=$1
    local times=()
    
    echo "## Git Sync Latency (bd update → commit → push → pull → import)" >> "$RESULTS_FILE"
    echo "" >> "$RESULTS_FILE"
    
    for i in $(seq 1 "$iterations"); do
        # Create a test issue
        test_id=$(./bd create "Latency test $i" -p 3 --json | jq -r '.id')
        
        # Measure: update → export → commit → push → pull (simulate)
        start=$(date +%s%N)
        
        # Update issue (triggers export after 30s debounce, but we'll force it)
        ./bd update "$test_id" --status in_progress >/dev/null 2>&1
        
        # Force immediate sync (bypasses debounce)
        ./bd sync >/dev/null 2>&1
        
        end=$(date +%s%N)
        
        # Calculate latency in milliseconds
        latency_ns=$((end - start))
        latency_ms=$((latency_ns / 1000000))
        times+=("$latency_ms")
        
        echo "Run $i: ${latency_ms}ms" >> "$RESULTS_FILE"
        
        # Cleanup
        ./bd close "$test_id" --reason "benchmark" >/dev/null 2>&1
    done
    
    # Calculate statistics
    IFS=$'\n' sorted=($(sort -n <<<"${times[*]}"))
    unset IFS
    
    count=${#sorted[@]}
    p50_idx=$((count / 2))
    p95_idx=$((count * 95 / 100))
    p99_idx=$((count * 99 / 100))
    
    echo "" >> "$RESULTS_FILE"
    echo "**Statistics (${iterations} runs):**" >> "$RESULTS_FILE"
    echo "- p50: ${sorted[$p50_idx]}ms" >> "$RESULTS_FILE"
    echo "- p95: ${sorted[$p95_idx]}ms" >> "$RESULTS_FILE"
    echo "- p99: ${sorted[$p99_idx]}ms" >> "$RESULTS_FILE"
    echo "" >> "$RESULTS_FILE"
}

# Function to measure Agent Mail latency
measure_agent_mail() {
    local iterations=$1
    local times=()
    
    echo "## Agent Mail Latency (send_message → fetch_inbox)" >> "$RESULTS_FILE"
    echo "" >> "$RESULTS_FILE"
    
    # Check if server is running
    if ! curl -s "$AGENT_MAIL_URL/health" >/dev/null 2>&1; then
        echo "⚠️ Agent Mail server not running. Skipping Agent Mail benchmark." >> "$RESULTS_FILE"
        echo "" >> "$RESULTS_FILE"
        return
    fi
    
    for i in $(seq 1 "$iterations"); do
        start=$(date +%s%N)
        
        # Send a message via HTTP API
        curl -s -X POST "$AGENT_MAIL_URL/api/messages" \
            -H "Authorization: Bearer $BEARER_TOKEN" \
            -H "Content-Type: application/json" \
            -d "{
                \"project_id\": \"beads\",
                \"sender\": \"agent-benchmark\",
                \"recipients\": [\"agent-test\"],
                \"subject\": \"Latency test $i\",
                \"body\": \"Benchmark message\",
                \"message_type\": \"notification\"
            }" >/dev/null 2>&1
        
        # Fetch inbox to complete round-trip
        curl -s "$AGENT_MAIL_URL/api/messages/beads/agent-test" \
            -H "Authorization: Bearer $BEARER_TOKEN" >/dev/null 2>&1
        
        end=$(date +%s%N)
        
        latency_ns=$((end - start))
        latency_ms=$((latency_ns / 1000000))
        times+=("$latency_ms")
        
        echo "Run $i: ${latency_ms}ms" >> "$RESULTS_FILE"
    done
    
    # Calculate statistics
    IFS=$'\n' sorted=($(sort -n <<<"${times[*]}"))
    unset IFS
    
    count=${#sorted[@]}
    p50_idx=$((count / 2))
    p95_idx=$((count * 95 / 100))
    p99_idx=$((count * 99 / 100))
    
    echo "" >> "$RESULTS_FILE"
    echo "**Statistics (${iterations} runs):**" >> "$RESULTS_FILE"
    echo "- p50: ${sorted[$p50_idx]}ms" >> "$RESULTS_FILE"
    echo "- p95: ${sorted[$p95_idx]}ms" >> "$RESULTS_FILE"
    echo "- p99: ${sorted[$p99_idx]}ms" >> "$RESULTS_FILE"
    echo "" >> "$RESULTS_FILE"
}

# Run benchmarks
ITERATIONS=10

echo "Running benchmarks ($ITERATIONS iterations each)..."

measure_git_sync "$ITERATIONS"
measure_agent_mail "$ITERATIONS"

echo "" >> "$RESULTS_FILE"
echo "## Conclusion" >> "$RESULTS_FILE"
echo "" >> "$RESULTS_FILE"
echo "Benchmark completed. See results above." >> "$RESULTS_FILE"

echo ""
echo "Results written to $RESULTS_FILE"
cat "$RESULTS_FILE"
