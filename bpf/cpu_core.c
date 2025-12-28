// go:build ignore
#include <linux/bpf.h>
#include "../bpf/headers/bpf_helpers.h"

typedef unsigned char u8;
typedef unsigned int u32;
typedef unsigned long long u64;

/* * STANDARD KERNEL LAYOUT (Raspberry Pi 5 / Generic AMD64)
 * This struct mimics the kernel's internal format for the sched_switch tracepoint.
 * The critical field 'prev_pid' is located at offset 24.
 */
struct sched_switch_core
{
    u8 pad[24];   // Padding: 8 bytes (common header) + 16 bytes (prev_comm char array)
    int prev_pid; // Offset 24: The PID of the process being switched OUT
    int prev_prio;
    long prev_state;
    u8 next_comm[16];
    int next_pid; // The PID of the process being switched IN
};

/* * MAP: start_times
 * Tracks when a process (PID) started using the CPU.
 * Key: PID (u32), Value: Timestamp in ns (u64)
 */
struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 10240);
} start_times SEC(".maps");

/* * MAP: cpu_usage
 * Accumulates total CPU time used by a process.
 * Key: PID (u32), Value: Total duration in ns (u64)
 */
struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 10240);
} cpu_usage SEC(".maps");

/*
 * HOOK: tracepoint/sched/sched_switch
 * Triggered every time the OS scheduler switches tasks.
 */
SEC("tracepoint/sched/sched_switch")
int handle_sched_switch(struct sched_switch_core *ctx)
{
    u64 now = bpf_ktime_get_ns();
    u32 prev_pid = ctx->prev_pid;
    u32 next_pid = ctx->next_pid;

    // LOGIC A: Handle the process leaving the CPU (prev_pid)
    if (prev_pid != 0)
    {
        // 1. Retrieve the time this process started running
        u64 *st = bpf_map_lookup_elem(&start_times, &prev_pid);
        if (st)
        {
            // 2. Calculate runtime duration (Current Time - Start Time)
            u64 delta = now - *st;

            // 3. Add this duration to the global accumulator for this PID
            u64 *total = bpf_map_lookup_elem(&cpu_usage, &prev_pid);
            if (total)
                __sync_fetch_and_add(total, delta);
            else
            {
                // First time seeing this PID, initialize entry
                u64 init = delta;
                bpf_map_update_elem(&cpu_usage, &prev_pid, &init, BPF_ANY);
            }
        }
    }

    // LOGIC B: Handle the process entering the CPU (next_pid)
    // We simply mark the current timestamp so we can calculate duration later.
    if (next_pid != 0)
    {
        bpf_map_update_elem(&start_times, &next_pid, &now, BPF_ANY);
    }
    return 0;
}

/*
 * HOOK: tracepoint/sched/sched_process_exit
 * Triggered when a process terminates.
 * Critical for preventing memory leaks in the BPF maps.
 */
SEC("tracepoint/sched/sched_process_exit")
int handle_process_exit(void *ctx)
{
    // Extract PID from the lower 32 bits of the helper return value
    u32 pid = bpf_get_current_pid_tgid() >> 32;

    // Clean up tracking data for the dead process
    bpf_map_delete_elem(&start_times, &pid);
    bpf_map_delete_elem(&cpu_usage, &pid);
    return 0;
}
char _license[] SEC("license") = "GPL";