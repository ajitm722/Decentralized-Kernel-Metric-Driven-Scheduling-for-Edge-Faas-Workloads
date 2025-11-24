#include "common.h"

/*
   MEMORY PRESSURE via DIRECT RECLAIM STALL TIME
   --------------------------------------------
   These tracepoints fire ONLY when the kernel must reclaim memory
   synchronously for a task. That means:
     "a process needed RAM, but there wasn't enough free right away."

   Measuring begin→end time gives real stall-time due to memory scarcity,
   which is a stronger scheduling signal than RAM% alone.
*/

// Global total stall time (ns) at index 0
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 1);
} mem_stall_ns SEC(".maps");

// PID -> reclaim start timestamp
struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 1024);
} start_times SEC(".maps");

/*
   vmscan/mm_vmscan_direct_reclaim_begin
   Fired when direct reclaim starts for current PID.
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
   vmscan/mm_vmscan_direct_reclaim_end
   Fired when direct reclaim ends.
   Delta = time task was stalled waiting for memory.
*/
SEC("tracepoint/vmscan/mm_vmscan_direct_reclaim_end")
int handle_reclaim_end(void *ctx)
{
    u32 pid = bpf_get_current_pid_tgid() & 0xFFFFFFFF;
    u64 *start = bpf_map_lookup_elem(&start_times, &pid);
    if (!start)
        return 0;

    u64 now = bpf_ktime_get_ns();
    u64 delta = now - *start;

    u32 key = 0;
    u64 *total = bpf_map_lookup_elem(&mem_stall_ns, &key);
    if (total)
    {
        __sync_fetch_and_add(total, delta);
    }

    bpf_map_delete_elem(&start_times, &pid);
    return 0;
}

char _license[] SEC("license") = "GPL";
