#include "common.h"

/*
    CPU COLLECTOR (With Cleanup)
    ----------------------------
    We measure CPU usage by tracking:
        - When a PID starts running     → record start time
        - When a PID stops running      → compute delta, accumulate CPU NS

    We ALSO listen for:
        sched/sched_process_exit
    so that when a process dies:
        - Remove PID entry from start_times
        - Remove PID entry from cpu_usage

    This prevents:
        - memory leaks
        - bogus CPU spikes due to PID reuse
*/

/* Timestamp when a PID started running */
struct start_time_t
{
    u64 ts;
};

/* PID → last start timestamp */
struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, struct start_time_t);
    __uint(max_entries, 10240);
} start_times SEC(".maps");

/* PID → accumulated CPU time */
struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 10240);
} cpu_usage SEC(".maps");

/*
    Tracepoint: sched/sched_switch
    ------------------------------
    Called when switching from prev → next task.
*/
SEC("tracepoint/sched/sched_switch")
int handle_sched_switch(struct trace_event_raw_sched_switch *ctx)
{
    u64 now = bpf_ktime_get_ns();

    /* --------------------------
       1. Account prev_pid CPU time
    --------------------------- */
    u32 prev_pid = ctx->prev_pid;
    if (prev_pid != 0)
    {
        struct start_time_t *st = bpf_map_lookup_elem(&start_times, &prev_pid);
        if (st)
        {
            u64 delta = now - st->ts;

            // Accumulate into cpu_usage
            u64 *total = bpf_map_lookup_elem(&cpu_usage, &prev_pid);
            if (total)
            {
                __sync_fetch_and_add(total, delta);
            }
            else
            {
                u64 init = delta;
                bpf_map_update_elem(&cpu_usage, &prev_pid, &init, BPF_ANY);
            }
        }
    }

    /* --------------------------
       2. Set new start time for next_pid
    --------------------------- */
    u32 next_pid = ctx->next_pid;
    if (next_pid != 0)
    {
        struct start_time_t new_start = {.ts = now};
        bpf_map_update_elem(&start_times, &next_pid, &new_start, BPF_ANY);
    }

    return 0;
}

/*
    Tracepoint: sched/sched_process_exit
    ------------------------------------
    We do NOT rely on the kernel's event struct.
    Instead, we use bpf_get_current_pid_tgid().

    This ALWAYS gives the PID of the exiting process.
*/
SEC("tracepoint/sched/sched_process_exit")
int handle_process_exit(void *ctx)
{
    u32 pid = bpf_get_current_pid_tgid() & 0xFFFFFFFF;

    // Safe cleanup of both maps
    bpf_map_delete_elem(&start_times, &pid);
    bpf_map_delete_elem(&cpu_usage, &pid);

    return 0;
}

char _license[] SEC("license") = "GPL";
