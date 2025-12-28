// go:build ignore
#include <linux/bpf.h>
#include "../bpf/headers/bpf_helpers.h"

typedef unsigned char u8;
typedef unsigned int u32;

/* * STANDARD THERMAL LAYOUT
 * Used by RPi 5. Temperature field is at offset 20.
 */
struct thermal_core
{
    u8 pad[8];                   // Common header
    u32 __data_loc_thermal_zone; // Pointer offset for zone name string
    int id;
    int temp_prev;
    int temp; // Offset 20: Temp in milli-Celsius
};

/* MAPS */
// Stores latest temperature for the zone (Key 0)
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);
    __type(value, u32);
    __uint(max_entries, 1);
} zone_temps SEC(".maps");

// Stores the Name of the thermal zone (e.g. "CPU-therm")
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);
    __type(value, char[16]);
    __uint(max_entries, 1);
} zone_names SEC(".maps");

// Flag to ensure we only capture the name once to save cycles
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

    // Calculate pointer to the variable-length string name
    u32 offset = ctx->__data_loc_thermal_zone & 0xFFFF;
    const char *zone_ptr = (const char *)ctx + offset;
    char namebuf[16];

    // Read kernel string safely into buffer
    bpf_probe_read_str(namebuf, sizeof(namebuf), zone_ptr);

    // Initialization check: Store the name only if we haven't yet
    u32 *count_ptr = bpf_map_lookup_elem(&zone_count, &zero);
    if (count_ptr && *count_ptr == 0)
    {
        bpf_map_update_elem(&zone_names, &zero, &namebuf, BPF_ANY);
        u32 newcount = 1;
        bpf_map_update_elem(&zone_count, &zero, &newcount, BPF_ANY);
    }

    // Always update the latest temperature
    bpf_map_update_elem(&zone_temps, &zero, &temp_mc, BPF_ANY);
    return 0;
}
char _license[] SEC("license") = "GPL";