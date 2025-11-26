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
// TempReading holds thermal zone data for streaming.

type TempReading struct {
	TempC  float64
	Status string
	Zone   string
}

// StartTEMPCollector starts the eBPF thermal collector and returns a TempReading channel.
func StartTEMPCollector() (<-chan TempReading, func(), error) {

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	var objs thermal_collectorObjects
	if err := loadThermal_collectorObjects(&objs, nil); err != nil {
		return nil, nil, fmt.Errorf("loading thermal objects: %v", err)
	}

	tp, err := link.Tracepoint("thermal", "thermal_temperature", objs.HandleThermalTemp, nil)
	if err != nil {
		objs.Close()
		return nil, nil, fmt.Errorf("attach thermal tracepoint: %v", err)
	}

	cleanup := func() {
		tp.Close()
		objs.Close()
	}

	out := make(chan TempReading)

	go func() {
		defer close(out)

		var key uint32 = 0
		var tempMC uint32
		var zoneCount uint32
		var namebuf [16]byte

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {

			// --- If no zone has been detected yet, send defaults ---
			if err := objs.ZoneCount.Lookup(&key, &zoneCount); err != nil || zoneCount == 0 {
				out <- TempReading{
					TempC:  0.0,
					Status: "UNAVAILABLE",
					Zone:   "unknown",
				}
				continue
			}

			// --- If zone exists, try reading actual data ---
			if err := objs.ZoneTemps.Lookup(&key, &tempMC); err != nil {
				out <- TempReading{
					TempC:  0.0,
					Status: "UNAVAILABLE",
					Zone:   "unknown",
				}
				continue
			}

			if err := objs.ZoneNames.Lookup(&key, &namebuf); err != nil {
				out <- TempReading{
					TempC:  0.0,
					Status: "UNAVAILABLE",
					Zone:   "unknown",
				}
				continue
			}

			zone := strings.Trim(string(namebuf[:]), "\x00")
			tempC := float64(tempMC) / 1000.0

			critical := 103.0
			pressure := tempC / critical
			status := "SAFE"
			if pressure >= 0.8 {
				status = "HOT"
			} else if pressure >= 0.6 {
				status = "WARM"
			}

			out <- TempReading{
				TempC:  tempC,
				Status: status,
				Zone:   zone,
			}
		}

	}()

	return out, cleanup, nil
}

func runTempWatch(cmd *cobra.Command, args []string) {

	tempStream, cleanup, err := StartTEMPCollector()
	if err != nil {
		log.Fatalf("thermal collector init error: %v", err)
	}
	defer cleanup()

	fmt.Println("Collecting thermal data… CTRL+C to stop")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case r := <-tempStream:
			fmt.Printf("[%s] %.1f°C → %s\n",
				r.Zone, r.TempC, r.Status)

		case <-stop:
			fmt.Println("Stopping temp collector…")
			return
		}
	}
}
