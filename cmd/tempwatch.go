package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/spf13/cobra"
)

var tempwatchCmd = &cobra.Command{
	Use:   "tempwatch",
	Short: "Collect thermal metrics through eBPF thermal_temperature tracepoint",
	Run:   runTempWatch,
}

func init() {
	rootCmd.AddCommand(tempwatchCmd)
}

/*
   Generate Go bindings for thermal_collector.c
*/
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go thermal_collector ../bpf/thermal_collector.c -- -I../bpf/headers

/*
runTempWatch
-------------
Userspace collector for thermal metrics.

Reads:

	zone_count[0]   → whether the zone name was recorded
	zone_names[0]   → thermal zone name (string)
	zone_temps[0]   → latest temperature (millidegree Celsius)

Only one zone is tracked (the first one observed), matching the eBPF logic.
*/
func runTempWatch(cmd *cobra.Command, args []string) {

	// Allow eBPF program + maps to lock memory
	if err := rlimit.RemoveMemlock(); err != nil {
		log.Fatalf("rlimit error: %v", err)
	}

	// Load thermal collector objects
	var objs thermal_collectorObjects
	if err := loadThermal_collectorObjects(&objs, nil); err != nil {
		log.Fatalf("loading objects: %v", err)
	}
	defer objs.Close()

	// Attach BPF program to tracepoint
	tp, err := link.Tracepoint("thermal", "thermal_temperature", objs.HandleThermalTemp, nil)
	if err != nil {
		log.Fatalf("cannot attach thermal tracepoint: %v", err)
	}
	defer tp.Close()

	fmt.Println("Collecting thermal data (via tracepoint)… CTRL+C to stop")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var key uint32 = 0
	var temp_mc uint32
	var zoneCount uint32
	var namebuf [16]byte

	for {
		select {
		case <-ticker.C:

			// Check if the zone name has been recorded yet
			if err := objs.ZoneCount.Lookup(&key, &zoneCount); err != nil {
				fmt.Println("No thermal data yet…")
				continue
			}
			if zoneCount == 0 {
				fmt.Println("Waiting for first thermal event…")
				continue
			}

			// Read temperature for zone 0
			if err := objs.ZoneTemps.Lookup(&key, &temp_mc); err != nil {
				continue
			}

			// Read stored zone name
			if err := objs.ZoneNames.Lookup(&key, &namebuf); err != nil {
				continue
			}
			zoneName := strings.Trim(string(namebuf[:]), "\x00")

			// Convert to Celsius
			tempC := float64(temp_mc) / 1000.0

			// Simple status logic
			criticalTemp := 103.0 // generic ARM thermal limit for now - 103 C is the critical temp on intel i7
			pressure := tempC / criticalTemp

			status := "SAFE"
			if pressure >= 0.8 {
				status = "HOT"
			} else if pressure >= 0.6 {
				status = "WARM"
			}

			// Output
			fmt.Printf("[%s] %.1f°C (pressure=%.2f) → %s\n",
				zoneName, tempC, pressure, status)

		case <-stop:
			fmt.Println("Stopping tempwatch…")
			return
		}
	}
}
