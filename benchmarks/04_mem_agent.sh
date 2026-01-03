#!/bin/bash
# ==========================================
# 04_mem_agent.sh
# Purpose: AGENT Memory Stress (With Agent)
# ==========================================

# --- Configuration ---
RUNS=10
DOCKER_IMAGE="alexeiled/stress-ng:latest"
AGENT_BIN="ebpf_edge_arm64_p"

# WORKLOAD: Must match baseline exactly
STRESS_ARGS="--vm 4 --vm-bytes 512M --vm-ops 3000"

# !!! UPDATE THIS IP !!!
PEER_IPS="192.168.0.250,192.168.0.x"
AGENT_CMD="./ebpf_edge_arm64_p peer --peers=$PEER_IPS"

# --- Start Agent ---
echo ">>> Ensuring no old agents are running..."
sudo pkill -f $AGENT_BIN
sleep 2

echo ">>> Starting Agent..."
sudo $AGENT_CMD > /dev/null 2>&1 &
AGENT_PID=$!

echo ">>> Agent PID: $AGENT_PID"
echo ">>> Stabilizing for 5 seconds..."
sleep 5

if ! ps -p $AGENT_PID > /dev/null; then
    echo "CRITICAL ERROR: Agent died immediately."
    exit 1
fi

echo ""
echo "========================================================"
echo " PHASE 2: MEMORY AGENT BENCHMARK (N=$RUNS)"
echo " Workload: 4 Workers @ 512MB each "
echo "========================================================"

total_time=0
min_time=9999
max_time=0

for i in $(seq 1 $RUNS); do
    val=$({ /usr/bin/time -f "%e" docker run --rm \
        --name rq2_mem_agent \
        $DOCKER_IMAGE \
        $STRESS_ARGS \
        --metrics-brief --quiet 2>&1 >/dev/null; } | tail -n1)

    if ! [[ $val =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
         echo "   Error on run $i: Output was '$val'"
         continue
    fi

    echo "   Run $i: $val seconds"

    total_time=$(awk "BEGIN {print $total_time + $val}")
    min_time=$(awk "BEGIN {if ($val < $min_time) print $val; else print $min_time}")
    max_time=$(awk "BEGIN {if ($val > $max_time) print $val; else print $max_time}")

    sleep 3
done

# --- Cleanup ---
echo ""
echo ">>> Stopping Agent..."
sudo kill $AGENT_PID
wait $AGENT_PID 2>/dev/null

# --- Results ---
avg_time=$(awk "BEGIN {printf \"%.4f\", $total_time / $RUNS}")

echo ""
echo "========================================================"
echo " MEMORY AGENT RESULTS"
echo "========================================================"
printf "%-10s | %-10s | %-10s\n" "Avg (s)" "Min (s)" "Max (s)"
echo "----------------------------------------"
printf "%-10s | %-10s | %-10s\n" "$avg_time" "$min_time" "$max_time"
echo "========================================================"