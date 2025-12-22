// go:build ignore
#include <linux/bpf.h>
#include "../bpf/headers/bpf_helpers.h"

typedef unsigned char u8;
typedef unsigned int u32;
typedef unsigned long long u64;

/* TEGRA LAYOUT (Jetson Orin Nano)
   prev_comm @ 12, prev_pid @ 28
*/
struct sched_switch_tegra
{
    u8 pad[28];   // 12 bytes header + 16 bytes prev_comm
    int prev_pid; // Offset 28
    int prev_prio;
    int pad_gap; // Extra gap seen in format
    long prev_state;
    u8 next_comm[16];
    int next_pid;
};

// ... (Exact same Maps and Logic as above) ...
// Copy the MAP definitions and SEC functions from cpu_core.c exactly.
// JUST change the struct name in handle_sched_switch argument:
// int handle_sched_switch(struct sched_switch_tegra *ctx)

/* --- FULL COPY FOR SAFETY --- */
struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 10240);
} start_times SEC(".maps");

struct
{
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32);
    __type(value, u64);
    __uint(max_entries, 10240);
} cpu_usage SEC(".maps");

SEC("tracepoint/sched/sched_switch")
int handle_sched_switch(struct sched_switch_tegra *ctx)
{
    u64 now = bpf_ktime_get_ns();
    u32 prev_pid = ctx->prev_pid;
    u32 next_pid = ctx->next_pid;

    if (prev_pid != 0)
    {
        u64 *st = bpf_map_lookup_elem(&start_times, &prev_pid);
        if (st)
        {
            u64 delta = now - *st;
            u64 *total = bpf_map_lookup_elem(&cpu_usage, &prev_pid);
            if (total)
                __sync_fetch_and_add(total, delta);
            else
            {
                u64 init = delta;
                bpf_map_update_elem(&cpu_usage, &prev_pid, &init, BPF_ANY);
            }
        }
    }
    if (next_pid != 0)
    {
        bpf_map_update_elem(&start_times, &next_pid, &now, BPF_ANY);
    }
    return 0;
}

SEC("tracepoint/sched/sched_process_exit")
int handle_process_exit(void *ctx)
{
    u32 pid = bpf_get_current_pid_tgid() >> 32;
    bpf_map_delete_elem(&start_times, &pid);
    bpf_map_delete_elem(&cpu_usage, &pid);
    return 0;
}
char _license[] SEC("license") = "GPL";
