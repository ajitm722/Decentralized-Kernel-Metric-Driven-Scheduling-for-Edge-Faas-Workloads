// go:build ignore
#include <linux/bpf.h>
#include "../bpf/headers/bpf_helpers.h"

typedef unsigned char u8;
typedef unsigned int u32;

/* * JETSON TEGRA THERMAL LAYOUT
 * Used by Orin Nano. Temperature field is at offset 24.
 */
struct thermal_tegra
{
    u8 pad[12];                  // Header is larger (12 bytes vs 8)
    u32 __data_loc_thermal_zone; // Offset 12
    int id;                      // Offset 16
    int temp_prev;               // Offset 20
    int temp;                    // Offset 24: Temp in milli-Celsius
};

/* MAPS */
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
int handle_thermal_temp(struct thermal_tegra *ctx)
{
    u32 zero = 0;
    u32 temp_mc = ctx->temp;

    // 1. Read Zone Name (Using Tegra-specific offset calculation)
    u32 offset = ctx->__data_loc_thermal_zone & 0xFFFF;
    const char *zone_ptr = (const char *)ctx + offset;
    char namebuf[16];
    bpf_probe_read_str(namebuf, sizeof(namebuf), zone_ptr);

    // 2. Store Name (Once)
    u32 *count_ptr = bpf_map_lookup_elem(&zone_count, &zero);
    if (count_ptr && *count_ptr == 0)
    {
        bpf_map_update_elem(&zone_names, &zero, &namebuf, BPF_ANY);
        u32 newcount = 1;
        bpf_map_update_elem(&zone_count, &zero, &newcount, BPF_ANY);
    }

    // 3. Store Temp
    bpf_map_update_elem(&zone_temps, &zero, &temp_mc, BPF_ANY);
    return 0;
}
char _license[] SEC("license") = "GPL";