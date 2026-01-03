#!/bin/bash
# ==========================================
# 03_mem_baseline.sh
# Purpose: BASELINE Memory Stress (No Agent)
# Impact: High Memory Churn
# ==========================================

# --- Configuration ---
RUNS=10
DOCKER_IMAGE="alexeiled/stress-ng:latest"
AGENT_BIN="ebpf_edge_arm64_p"

# WORKLOAD: 4 Workers, 512MB each , 3000 ops total
STRESS_ARGS="--vm 4 --vm-bytes 512M --vm-ops 3000"

# --- Safety Check ---
if pgrep -f "$AGENT_BIN" > /dev/null; then
    echo "ERROR: The agent '$AGENT_BIN' is currently running!"
    echo "Please stop it before running the baseline."
    exit 1
fi

echo "========================================================"
echo " PHASE 1: MEMORY BASELINE (N=$RUNS)"
echo " Workload: 4 Workers @ 512MB each "
echo "========================================================"

total_time=0
min_time=9999
max_time=0

for i in $(seq 1 $RUNS); do
    # Run Docker and capture wall clock time
    val=$({ /usr/bin/time -f "%e" docker run --rm \
        --name rq2_mem_base \
        $DOCKER_IMAGE \
        $STRESS_ARGS \
        --metrics-brief --quiet 2>&1 >/dev/null; } | tail -n1)

    # Sanity check output
    if ! [[ $val =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
         echo "   Error on run $i: Output was '$val'"
         continue
    fi

    echo "   Run $i: $val seconds"

    # Math helpers
    total_time=$(awk "BEGIN {print $total_time + $val}")
    min_time=$(awk "BEGIN {if ($val < $min_time) print $val; else print $min_time}")
    max_time=$(awk "BEGIN {if ($val > $max_time) print $val; else print $max_time}")

    sleep 3
done

# Calculate Average
avg_time=$(awk "BEGIN {printf \"%.4f\", $total_time / $RUNS}")

echo ""
echo "========================================================"
echo " MEMORY BASELINE RESULTS"
echo "========================================================"
printf "%-10s | %-10s | %-10s\n" "Avg (s)" "Min (s)" "Max (s)"
echo "----------------------------------------"
printf "%-10s | %-10s | %-10s\n" "$avg_time" "$min_time" "$max_time"
echo "========================================================"
