package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var tempwatchCmd = &cobra.Command{
	Use:   "tempwatch",
	Short: "Monitor CPU + PCH temperature from sysfs with clear semantic thresholds",
	Run:   runTempWatch,
}

func init() {
	rootCmd.AddCommand(tempwatchCmd)
}

// -----------------------------------------------------------------------------
// readInt: Small helper to read an integer from sysfs.
// thermal_zone*/temp always contains temps in millidegrees Celsius (e.g. 43000)
// -----------------------------------------------------------------------------
func readInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// -----------------------------------------------------------------------------
// locate CPU and PCH thermal sensors
//
// Modern Intel systems expose many thermal zones, but only two are meaningful:
//
//  1. x86_pkg_temp
//     - This is the *actual CPU package thermal diode*.
//     - It directly controls CPU thermal throttling.
//     - This temperature determines if the CPU can keep running workloads.
//
//  2. pch_cannonlake
//     - Temperature of the Intel Platform Controller Hub (PCH).
//     - PCH controls USB, PCIe, NVMe, WiFi, SATA.
//     - Overheating reduces I/O bandwidth → heavy tasks fail.
//
// All other thermal zones (iwlwifi, acpitz, B0D4, etc.) are irrelevant for
// embedded system task scheduling. Only these two predict real performance limits.
// -----------------------------------------------------------------------------

type ThermalSensors struct {
	CPUPath string // path to /thermal_zone*/temp for CPU package
	PCHPath string // path to /thermal_zone*/temp for PCH sensor
}

func findThermalSensors() (*ThermalSensors, error) {
	s := &ThermalSensors{}

	zones, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		return nil, err
	}

	for _, z := range zones {
		tbuf, err := os.ReadFile(filepath.Join(z, "type"))
		if err != nil {
			continue
		}
		t := strings.TrimSpace(string(tbuf))

		switch t {
		case "x86_pkg_temp":
			s.CPUPath = filepath.Join(z, "temp")
		case "pch_cannonlake":
			s.PCHPath = filepath.Join(z, "temp")
		}
	}

	if s.CPUPath == "" {
		return nil, fmt.Errorf("CPU temperature sensor (x86_pkg_temp) not found")
	}
	if s.PCHPath == "" {
		return nil, fmt.Errorf("PCH temperature sensor (pch_cannonlake) not found")
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// runTempWatch
// Continuously monitors CPU + PCH temperature and applies OR-based thresholds.
//
// IMPORTANT LOGIC:
//
//	Each component is evaluated independently:
//	   CPU crossing HOT → device is HOT.
//	   PCH crossing HOT → device is HOT.
//	   CPU OR PCH crossing WARM → device is WARM.
//
//	This OR-based logic matches embedded scheduling because:
//
//	   - If CPU is hot → cannot run compute tasks.
//	   - If PCH is hot → cannot handle I/O-heavy tasks.
//	   - Leader node must avoid sending tasks when ANY critical subsystem overheats.
//
// -----------------------------------------------------------------------------
func runTempWatch(cmd *cobra.Command, args []string) {

	sensors, err := findThermalSensors()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	fmt.Println("Using sensors:")
	fmt.Printf(" CPU: %s  (x86_pkg_temp)\n", sensors.CPUPath)
	fmt.Printf(" PCH: %s  (pch_cannonlake)\n\n", sensors.PCHPath)

	// Conservative safe thermal reference limits:
	// These values are widely used in embedded systems and Intel platforms.
	criticalCPU := 100.0 // °C: CPUs usually throttle 90–100
	criticalPCH := 90.0  // °C: PCH thermal limit is usually ~85–95

	fmt.Println("Collecting thermal metrics... CTRL+C to stop")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:

			rawCPU, _ := readInt(sensors.CPUPath)
			rawPCH, _ := readInt(sensors.PCHPath)

			cpuC := float64(rawCPU) / 1000.0
			pchC := float64(rawPCH) / 1000.0

			// Normalize to pressure values 0.0 → 1.0
			cpuPressure := cpuC / criticalCPU
			pchPressure := pchC / criticalPCH

			//------------------------------------------------------------------
			// DEVICE PRESSURE LOGIC (OR–BASED)
			//
			// If either subsystem overheats, the whole device becomes unreliable.
			// This matches real embedded workload scheduling:
			//    - CPU hot? cannot compute reliably.
			//    - PCH hot? cannot load models / access peripherals reliably.
			//------------------------------------------------------------------
			status := "SAFE"

			hot := cpuPressure >= 0.8 || pchPressure >= 0.8
			warm := cpuPressure >= 0.6 || pchPressure >= 0.6

			if hot {
				status = "HOT"
			} else if warm {
				status = "WARM"
			}

			fmt.Printf(
				"CPU: %.1f°C (%.2f) | PCH: %.1f°C (%.2f) | STATUS: %s\n",
				cpuC, cpuPressure,
				pchC, pchPressure,
				status,
			)

		case <-stop:
			fmt.Println("Stopping tempwatch...")
			return
		}
	}
}
