package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/spf13/cobra"
)

var cpuwatchCmd = &cobra.Command{
	Use:   "cpuwatch",
	Short: "Collect total node-wide CPU usage using sched_switch (with cleanup)",
	Run:   runCPUWatch,
}

func init() {
	rootCmd.AddCommand(cpuwatchCmd)
}

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target native cpu_collector ../bpf/cpu_collector.c -- -I../bpf/headers

func runCPUWatch(cmd *cobra.Command, args []string) {

	if err := rlimit.RemoveMemlock(); err != nil {
		log.Fatalf("rlimit error: %v", err)
	}

	// Load BPF objects
	var objs cpu_collectorObjects
	if err := loadCpu_collectorObjects(&objs, nil); err != nil {
		log.Fatalf("loading objects: %v", err)
	}
	defer objs.Close()

	// Attach CPU accounting program
	hSwitch, err := link.Tracepoint("sched", "sched_switch", objs.HandleSchedSwitch, nil)
	if err != nil {
		log.Fatalf("Failed to attach sched_switch: %v", err)
	}
	defer hSwitch.Close()

	// Attach cleanup program
	hExit, err := link.Tracepoint("sched", "sched_process_exit", objs.HandleProcessExit, nil)
	if err != nil {
		log.Fatalf("Failed to attach sched_process_exit: %v", err)
	}
	defer hExit.Close()

	fmt.Println("Collecting TOTAL CPU usage (with cleanup)... CTRL+C to stop")

	lastCPU := make(map[uint32]uint64)
	poll := 500 * time.Millisecond
	intervalNS := uint64(poll.Nanoseconds())

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:

			// Sum of all CPU deltas
			var totalDelta uint64 = 0

			iter := objs.CpuUsage.Iterate()
			var pid uint32
			var ns uint64

			for iter.Next(&pid, &ns) {
				prev := lastCPU[pid]
				delta := ns - prev
				lastCPU[pid] = ns
				totalDelta += delta
			}

			if err := iter.Err(); err != nil {
				log.Printf("Iterator error: %v", err)
				continue
			}

			// Convert to CPU%
			cpuPercent := (float64(totalDelta) / float64(intervalNS)) * 100.0

			fmt.Printf("CPU: %.2f%%\n", cpuPercent)

		case <-stop:
			fmt.Println("Stopping cpu collector...")
			return
		}
	}
}
