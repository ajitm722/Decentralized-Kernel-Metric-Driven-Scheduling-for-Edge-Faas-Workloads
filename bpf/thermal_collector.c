#include "common.h"

/*
    Simplified Thermal Collector (Single-Zone Model)
    ------------------------------------------------
    This eBPF program listens to:
        tracepoint/thermal/thermal_temperature

    The kernel provides thermal information for potentially many zones
    (CPU, GPU, PCH, WiFi, battery, etc.), but for small ARM/embedded systems:

       • usually only 1 or 2 zones exist
       • the first zone that emits an event is generally the CPU thermal diode

    Therefore, the design here:
        - Stores ONLY ONE zone (index 0)
        - Records the zone name ONLY ONCE
        - Always updates the temperature for index 0

    The userspace collector treats this zone as “the primary thermal source”
    for the node.
*/

/* BPF map: temperature of the tracked zone (millidegree Celsius) */
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);   // Always 0
    __type(value, u32); // Temperature in milli-Celsius
    __uint(max_entries, 1);
} zone_temps SEC(".maps");

/* BPF map: name of the tracked zone (string) */
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32); // Always 0
    __type(value, char[16]);
    __uint(max_entries, 1);
} zone_names SEC(".maps");

/* BPF map: number of stored zones (0 or 1 in this simplified model) */
struct
{
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, u32);   // Always 0
    __type(value, u32); // Count (0 → uninitialized, 1 → initialized)
    __uint(max_entries, 1);
} zone_count SEC(".maps");

/*
    BTF struct from vmlinux.h (verified on your kernel):

    struct trace_event_raw_thermal_temperature {
        struct trace_entry ent;
        u32 __data_loc_thermal_zone;
        int id;
        int temp_prev;
        int temp;
        char __data[0];
    };
*/
SEC("tracepoint/thermal/thermal_temperature")
int handle_thermal_temp(struct trace_event_raw_thermal_temperature *ctx)
{
    u32 zero = 0;

    /* --------------------------------------------------------------------
       STEP 1 — Extract latest temperature value (millidegree Celsius)
       -------------------------------------------------------------------- */
    u32 temp_mc = ctx->temp; // Example: 43000 → 43.0°C

    /* --------------------------------------------------------------------
       STEP 2 — Determine zone name (only stored once)
                  The data_loc field is a relative offset into __data[]
       -------------------------------------------------------------------- */
    u32 offset = ctx->__data_loc_thermal_zone & 0xFFFF;
    const char *zone_ptr = (const char *)ctx + offset;

    char namebuf[16];
    bpf_probe_read_str(namebuf, sizeof(namebuf), zone_ptr);

    /* --------------------------------------------------------------------
       STEP 3 — Check if zone name is already stored
       -------------------------------------------------------------------- */
    u32 *count_ptr = bpf_map_lookup_elem(&zone_count, &zero);
    if (!count_ptr)
        return 0; // Should not happen

    u32 count = *count_ptr;

    /* --------------------------------------------------------------------
       STEP 4 — If this is the FIRST event, store the zone name
                (count == 0 means we have not recorded any zone yet)
       -------------------------------------------------------------------- */
    if (count == 0)
    {
        // Store zone name into index 0
        bpf_map_update_elem(&zone_names, &zero, &namebuf, BPF_ANY);

        // Mark count = 1 (initialized)
        u32 newcount = 1;
        bpf_map_update_elem(&zone_count, &zero, &newcount, BPF_ANY);
    }

    /* --------------------------------------------------------------------
       STEP 5 — Always update latest temperature for zone 0
       -------------------------------------------------------------------- */
    bpf_map_update_elem(&zone_temps, &zero, &temp_mc, BPF_ANY);

    return 0;
}

char _license[] SEC("license") = "GPL";
