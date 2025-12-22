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

func StartTEMPCollector() (<-chan TempReading, func(), error) {

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	var (
		tp       link.Link
		cleanup  func()
		mapTemps *ebpf.Map
		mapNames *ebpf.Map
		mapCount *ebpf.Map
		prog     *ebpf.Program
	)

	// 1. Detect Kernel
	var uname syscall.Utsname
	syscall.Uname(&uname)
	var releaseBuf []byte
	for _, b := range uname.Release {
		if b == 0 {
			break
		}
		releaseBuf = append(releaseBuf, byte(b))
	}
	release := string(releaseBuf)
	log.Printf("Kernel Detected (Temp): %s", release)

	// 2. Load the Right Object
	if strings.Contains(release, "tegra") {
		var objs thermal_tegraObjects
		if err := loadThermal_tegraObjects(&objs, nil); err != nil {
			return nil, nil, fmt.Errorf("load tegra temp: %v", err)
		}
		prog = objs.HandleThermalTemp
		mapTemps = objs.ZoneTemps
		mapNames = objs.ZoneNames
		mapCount = objs.ZoneCount
		cleanup = func() { objs.Close() }
	} else {
		var objs thermal_coreObjects
		if err := loadThermal_coreObjects(&objs, nil); err != nil {
			return nil, nil, fmt.Errorf("load core temp: %v", err)
		}
		prog = objs.HandleThermalTemp
		mapTemps = objs.ZoneTemps
		mapNames = objs.ZoneNames
		mapCount = objs.ZoneCount
		cleanup = func() { objs.Close() }
	}

	// 3. Attach Tracepoint
	var err error
	tp, err = link.Tracepoint("thermal", "thermal_temperature", prog, nil)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("attach temp tracepoint: %v", err)
	}

	finalCleanup := func() {
		tp.Close()
		cleanup()
	}

	out := make(chan TempReading)

	go func() {
		defer close(out)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			var key uint32 = 0
			var tempMC uint32
			var zoneCount uint32
			var namebuf [16]byte

			if err := mapCount.Lookup(&key, &zoneCount); err != nil || zoneCount == 0 {
				continue
			}
			if err := mapTemps.Lookup(&key, &tempMC); err != nil {
				continue
			}
			if err := mapNames.Lookup(&key, &namebuf); err != nil {
				continue
			}

			zone := strings.Trim(string(namebuf[:]), "\x00")
			tempC := float64(tempMC) / 1000.0

			status := "SAFE"
			if tempC > 80.0 {
				status = "HOT"
			} else if tempC > 60.0 {
				status = "WARM"
			}

			out <- TempReading{TempC: tempC, Status: status, Zone: zone}
		}
	}()

	return out, finalCleanup, nil
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
