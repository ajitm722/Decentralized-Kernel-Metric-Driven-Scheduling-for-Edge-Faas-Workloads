#!/bin/bash
# ==========================================
# 01_baseline.sh
# Purpose: Measure clean system performance (No Agent)
# ==========================================

# --- Configuration ---
RUNS=10
DOCKER_IMAGE="alexeiled/stress-ng:latest"
CPU_OPS="1000"
AGENT_BIN="ebpf_edge_arm64_p" # Used to check for conflicts

# --- Safety Check ---
if pgrep -f "$AGENT_BIN" > /dev/null; then
    echo "ERROR: The agent '$AGENT_BIN' is currently running!"
    echo "Please stop it before running the baseline to ensure clean data."
    exit 1
fi

echo "========================================================"
echo " PHASE 1: BASELINE BENCHMARK (N=$RUNS)"
echo "========================================================"

total_time=0
min_time=9999
max_time=0

for i in $(seq 1 $RUNS); do
    # Run Docker and capture ONLY the wall clock time
    val=$({ /usr/bin/time -f "%e" docker run --rm \
        --name rq2_baseline \
        $DOCKER_IMAGE \
        --cpu 0 --cpu-method matrixprod --cpu-ops $CPU_OPS \
        --metrics-brief --quiet 2>&1 >/dev/null; } | tail -n1)

    # Sanity check
    if ! [[ $val =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
         echo "   Error on run $i: Output was '$val'"
         continue
    fi

    echo "   Run $i: $val seconds"

    # Math helpers
    total_time=$(awk "BEGIN {print $total_time + $val}")
    min_time=$(awk "BEGIN {if ($val < $min_time) print $val; else print $min_time}")
    max_time=$(awk "BEGIN {if ($val > $max_time) print $val; else print $max_time}")
    
    sleep 2
done

# Calculate Average
avg_time=$(awk "BEGIN {printf \"%.4f\", $total_time / $RUNS}")

echo ""
echo "========================================================"
echo " BASELINE RESULTS"
echo "========================================================"
printf "%-10s | %-10s | %-10s\n" "Avg (s)" "Min (s)" "Max (s)"
echo "----------------------------------------"
printf "%-10s | %-10s | %-10s\n" "$avg_time" "$min_time" "$max_time"
echo "========================================================"
