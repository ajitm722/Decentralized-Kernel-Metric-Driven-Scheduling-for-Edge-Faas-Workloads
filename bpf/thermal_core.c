// go:build ignore
#include <linux/bpf.h>
#include "../bpf/headers/bpf_helpers.h"

typedef unsigned char u8;
typedef unsigned int u32;

/* STANDARD CORE LAYOUT (RPi 5 / AMD64) */
struct thermal_core
{
    u8 pad[8];                   // Common header (0-8)
    u32 __data_loc_thermal_zone; // Offset 8
    int id;                      // Offset 12
    int temp_prev;               // Offset 16
    int temp;                    // Offset 20
};

/* MAPS (Same as Tegra) */
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);
    __type(value, u32);
    __uint(max_entries, 1);
} zone_temps SEC(".maps");

struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);
    __type(value, char[16]);
    __uint(max_entries, 1);
} zone_names SEC(".maps");

struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);
    __type(value, u32);
    __uint(max_entries, 1);
} zone_count SEC(".maps");

SEC("tracepoint/thermal/thermal_temperature")
int handle_thermal_temp(struct thermal_core *ctx)
{
    u32 zero = 0;
    u32 temp_mc = ctx->temp;

    u32 offset = ctx->__data_loc_thermal_zone & 0xFFFF;
    const char *zone_ptr = (const char *)ctx + offset;
    char namebuf[16];
    bpf_probe_read_str(namebuf, sizeof(namebuf), zone_ptr);

    u32 *count_ptr = bpf_map_lookup_elem(&zone_count, &zero);
    if (count_ptr && *count_ptr == 0)
    {
        bpf_map_update_elem(&zone_names, &zero, &namebuf, BPF_ANY);
        u32 newcount = 1;
        bpf_map_update_elem(&zone_count, &zero, &newcount, BPF_ANY);
    }

    bpf_map_update_elem(&zone_temps, &zero, &temp_mc, BPF_ANY);
    return 0;
}
char _license[] SEC("license") = "GPL";