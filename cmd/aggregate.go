package cmd

import (
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/spf13/cobra"
)

// aggregateCmd is the Cobra command that runs all collectors together.
var aggregateCmd = &cobra.Command{
    Use:   "aggregate",
    Short: "Aggregate CPU, memory, and temperature metrics into a unified output",
    Run:   runAggregate,
}

func init() {
    rootCmd.AddCommand(aggregateCmd)
}

// runAggregate launches all collectors as goroutines, consumes their streams,
// and updates a shared MetricsSnapshot structure.
// This allows the aggregator to serve as a single producer of unified metrics.
func runAggregate(cmd *cobra.Command, args []string) {

    var snap MetricsSnapshot

    // ----------------------------------------------------
    // Start all collectors and get their cleanup functions.
    // ----------------------------------------------------
    cpuStream, cpuCleanup, err := StartCPUCollector()
    if err != nil {
        fmt.Println("CPU collector init error:", err)
        return
    }
    defer cpuCleanup()

    memStream, memCleanup, err := StartMEMCollector()
    if err != nil {
        fmt.Println("MEM collector init error:", err)
        return
    }
    defer memCleanup()

    tempStream, tempCleanup, err := StartTEMPCollector()
    if err != nil {
        fmt.Println("TEMP collector init error:", err)
        return
    }
    defer tempCleanup()

    // ----------------------------------------------------
    // Handle stop signal for clean termination.
    // ----------------------------------------------------
    stop := make(chan os.Signal, 1)
    signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

    // ----------------------------------------------------
    // CPU consumer goroutine
    // Each collector streams updates, and the aggregator just updates the snapshot.
    // ----------------------------------------------------
    go func() {
        for v := range cpuStream {
            snap.UpdateCPU(v)
        }
    }()

    // ----------------------------------------------------
    // Memory consumer goroutine
    // ----------------------------------------------------
    go func() {
        for v := range memStream {
            snap.UpdateMem(v)
        }
    }()

    // ----------------------------------------------------
    // Temperature consumer goroutine
    // ----------------------------------------------------
    go func() {
        for r := range tempStream {
            snap.UpdateTemp(r)
        }
    }()

    // ----------------------------------------------------
    // Main output loop
    // Reads a copy of the snapshot every second and prints it.
    // Later, this is where we will call gRPC to send the data.
    // ----------------------------------------------------
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    fmt.Println("Running aggregator... CTRL+C to stop")

    for {
        select {
        case <-ticker.C:
            s := snap.Read()

            fmt.Printf("[AGG] CPU=%.2f%%  MEM=%.2f%%  TEMP=%.1f°C (%s)  zone=%s\n",
                s.CPUPercent,
                s.MemPercent,
                s.TempC,
                s.TempStatus,
                s.ZoneName,
            )

        case <-stop:
            fmt.Println("Stopping aggregator...")
            return
        }
    }
}

