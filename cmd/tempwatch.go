package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/spf13/cobra"
)

var tempwatchCmd = &cobra.Command{
	Use:   "tempwatch",
	Short: "Collect thermal metrics (Auto-Detect Core vs Tegra)",
	Run:   runTempWatch,
}

func init() {
	rootCmd.AddCommand(tempwatchCmd)
}

// Generate BOTH binaries
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go thermal_core ../bpf/thermal_core.c -- -O2 -target bpf -I/usr/include/x86_64-linux-gnu
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go thermal_tegra ../bpf/thermal_tegra.c -- -O2 -target bpf -I/usr/include/x86_64-linux-gnu

type TempReading struct {
	TempC  float64
	Status string
	Zone   string
}

// thermalBPF abstracts the specific eBPF objects (Tegra vs Core)
type thermalBPF struct {
	prog     *ebpf.Program
	mapTemps *ebpf.Map
	mapNames *ebpf.Map
	mapCount *ebpf.Map
	cleanup  func()
}

func loadTegraBPF() (*thermalBPF, error) {
	var objs thermal_tegraObjects
	if err := loadThermal_tegraObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("load tegra temp: %v", err)
	}
	return &thermalBPF{
		prog:     objs.HandleThermalTemp,
		mapTemps: objs.ZoneTemps,
		mapNames: objs.ZoneNames,
		mapCount: objs.ZoneCount,
		cleanup:  func() { objs.Close() },
	}, nil
}

func loadCoreBPF() (*thermalBPF, error) {
	var objs thermal_coreObjects
	if err := loadThermal_coreObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("load core temp: %v", err)
	}
	return &thermalBPF{
		prog:     objs.HandleThermalTemp,
		mapTemps: objs.ZoneTemps,
		mapNames: objs.ZoneNames,
		mapCount: objs.ZoneCount,
		cleanup:  func() { objs.Close() },
	}, nil
}

func StartTEMPCollector() (<-chan TempReading, func(), error) {

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	// 1. Detect Kernel
	release := detectKernel()
	log.Printf("Kernel Detected (Temp): %s", release)

	// 2. Load the Right Object
	var loader func() (*thermalBPF, error)
	if strings.Contains(release, "tegra") {
		loader = loadTegraBPF
	} else {
		loader = loadCoreBPF
	}

	bpf, err := loader()
	if err != nil {
		return nil, nil, err
	}

	// 3. Attach Tracepoint
	tp, err := link.Tracepoint("thermal", "thermal_temperature", bpf.prog, nil)
	if err != nil {
		bpf.cleanup()
		return nil, nil, fmt.Errorf("attach temp tracepoint: %v", err)
	}

	finalCleanup := func() {
		tp.Close()
		bpf.cleanup()
	}

	out := make(chan TempReading)

	go pollThermalStats(out, bpf)

	return out, finalCleanup, nil
}

func pollThermalStats(out chan<- TempReading, bpf *thermalBPF) {
	defer close(out)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		var key uint32 = 0
		var tempMC uint32
		var zoneCount uint32
		var namebuf [16]byte

		// CRITICAL SECTION: Read BPF Maps for Thermal Data
		// 1. Check if the BPF program has actually detected a thermal zone (count > 0).
		if err := bpf.mapCount.Lookup(&key, &zoneCount); err != nil || zoneCount == 0 {
			continue
		}
		// 2. Retrieve the latest temperature reading (in millidegrees Celsius).
		if err := bpf.mapTemps.Lookup(&key, &tempMC); err != nil {
			continue
		}
		// 3. Retrieve the zone name (e.g., "CPU-therm").
		if err := bpf.mapNames.Lookup(&key, &namebuf); err != nil {
			continue
		}

		zone := strings.Trim(string(namebuf[:]), "\x00")
		// Convert millidegrees to degrees Celsius.
		tempC := float64(tempMC) / 1000.0

		// Determine status based on hardcoded safety thresholds.
		status := "SAFE"
		if tempC > 80.0 {
			status = "HOT"
		} else if tempC > 60.0 {
			status = "WARM"
		}

		out <- TempReading{TempC: tempC, Status: status, Zone: zone}
	}
}

func runTempWatch(cmd *cobra.Command, args []string) {
	stream, cleanup, err := StartTEMPCollector()
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()
	fmt.Println("Collecting Thermal... CTRL+C to stop")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case v := <-stream:
			fmt.Printf("[%s] %.1f°C (%s)\n", v.Zone, v.TempC, v.Status)
		case <-stop:
			return
		}
	}
}
