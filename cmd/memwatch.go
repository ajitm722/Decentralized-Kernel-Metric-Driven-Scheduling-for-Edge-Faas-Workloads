package cmd

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// --- CLI Command Setup ---
var memwatchCmd = &cobra.Command{
	Use:   "memwatch",
	Short: "Collect memory saturation via /proc/meminfo",
	Run:   runMemWatch,
}

func init() {
	rootCmd.AddCommand(memwatchCmd)
}

// --- The Collector Function ---
// StartMEMCollector starts a ticker that reads /proc/meminfo every 1s
// Returns: A channel of float64 representing Memory Usage %
func StartMEMCollector() (<-chan float64, func(), error) {
	out := make(chan float64)
	done := make(chan struct{})

	// Cleanup function to stop the polling routine
	cleanup := func() {
		close(done)
	}

	go func() {
		defer close(out)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				usage, err := getMemoryUsage()
				if err != nil {
					log.Printf("Error reading memory: %v", err)
					continue
				}
				out <- usage
			}
		}
	}()

	return out, cleanup, nil
}

// --- Helper: Parse /proc/meminfo ---
func getMemoryUsage() (float64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	var memTotal, memAvailable float64
	scanner := bufio.NewScanner(file)

	// We scan the file line by line looking for specific keys
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				memTotal, _ = strconv.ParseFloat(parts[1], 64)
			}
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				memAvailable, _ = strconv.ParseFloat(parts[1], 64)
			}
		}
		// Optimization: Stop reading once we have both values
		if memTotal > 0 && memAvailable > 0 {
			break
		}
	}

	if memTotal == 0 {
		return 0, fmt.Errorf("could not determine MemTotal")
	}

	// Calculation:
	// Saturation % = (Total - Available) / Total * 100
	usedPercent := ((memTotal - memAvailable) / memTotal) * 100.0
	return usedPercent, nil
}

// --- Run Handler ---
func runMemWatch(cmd *cobra.Command, args []string) {
	memStream, cleanup, err := StartMEMCollector()
	if err != nil {
		log.Fatalf("Init error: %v", err)
	}
	defer cleanup()

	logDebug("Collecting MEMORY usage... CTRL+C to stop")

	// Just for demo purposes, handle signal to exit cleanly
	// (In your main agent, this logic is likely handled elsewhere)
	// for v := range memStream { ... }
	for v := range memStream {
		logDebug("MEM Saturation: %.2f%%\n", v)
	}
}
