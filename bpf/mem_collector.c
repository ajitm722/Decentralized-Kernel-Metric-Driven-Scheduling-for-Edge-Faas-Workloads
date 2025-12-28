#include "common.h"

/*
   MEMORY PRESSURE via DIRECT RECLAIM STALL TIME
   --------------------------------------------
   Objective: Measure the time a process is forcibly paused by the kernel
   waiting for memory to be freed.
*/

// MAP: mem_stall_ns
// Aggregates total stall time across the entire node.
// Key: 0 (Global singleton), Value: Total stall time in nanoseconds.
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 1);
} mem_stall_ns SEC(".maps");

// MAP: start_times
// Temporary storage for the timestamp when a specific PID entered reclaim state.
struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 1024);
} start_times SEC(".maps");

/*
   HOOK: vmscan/mm_vmscan_direct_reclaim_begin
   Trigger: Fired immediately before the kernel attempts to reclaim pages synchronously.
   Action: Record the current timestamp.
*/
SEC("tracepoint/vmscan/mm_vmscan_direct_reclaim_begin")
int handle_reclaim_begin(void *ctx)
{
    u32 pid = bpf_get_current_pid_tgid() & 0xFFFFFFFF;
    u64 ts = bpf_ktime_get_ns();

    bpf_map_update_elem(&start_times, &pid, &ts, BPF_ANY);
    return 0;
}

/*
   HOOK: vmscan/mm_vmscan_direct_reclaim_end
   Trigger: Fired when the kernel finishes reclaiming pages.
   Action: Calculate duration and add to global stall counter.
*/
SEC("tracepoint/vmscan/mm_vmscan_direct_reclaim_end")
int handle_reclaim_end(void *ctx)
{
    u32 pid = bpf_get_current_pid_tgid() & 0xFFFFFFFF;

    // Retrieve the start time for this PID
    u64 *start = bpf_map_lookup_elem(&start_times, &pid);
    if (!start)
        return 0; // Missed the start event, ignore.

    u64 now = bpf_ktime_get_ns();
    u64 delta = now - *start;

    // Update the global accumulator (Index 0)
    u32 key = 0;
    u64 *total = bpf_map_lookup_elem(&mem_stall_ns, &key);
    if (total)
    {
        __sync_fetch_and_add(total, delta);
    }

    // Cleanup: Remove temporary start timestamp
    bpf_map_delete_elem(&start_times, &pid);
    return 0;
}

char _license[] SEC("license") = "GPL";