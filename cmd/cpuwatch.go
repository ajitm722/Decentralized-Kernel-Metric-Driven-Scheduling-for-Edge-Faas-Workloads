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

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go cpu_collector ../bpf/cpu_collector.c -- -I../bpf/headers

// StartCPUCollector starts the eBPF program and returns a channel that streams CPU usage (%) every poll.
func StartCPUCollector() (<-chan float64, func(), error) {

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	// Load BPF objects
	var objs cpu_collectorObjects
	if err := loadCpu_collectorObjects(&objs, nil); err != nil {
		return nil, nil, fmt.Errorf("loading objects: %v", err)
	}

	// Attach programs
	hSwitch, err := link.Tracepoint("sched", "sched_switch", objs.HandleSchedSwitch, nil)
	if err != nil {
		objs.Close()
		return nil, nil, fmt.Errorf("attach sched_switch: %v", err)
	}

	hExit, err := link.Tracepoint("sched", "sched_process_exit", objs.HandleProcessExit, nil)
	if err != nil {
		hSwitch.Close()
		objs.Close()
		return nil, nil, fmt.Errorf("attach sched_exit: %v", err)
	}

	// Cleanup function
	cleanup := func() {
		hSwitch.Close()
		hExit.Close()
		objs.Close()
	}

	// Stream channel
	out := make(chan float64)

	go func() {
		defer close(out)

		lastCPU := make(map[uint32]uint64)
		poll := 500 * time.Millisecond
		intervalNS := uint64(poll.Nanoseconds())
		ticker := time.NewTicker(poll)
		defer ticker.Stop()

		for range ticker.C {

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

			cpuPercent := (float64(totalDelta) / float64(intervalNS)) * 100.0

			out <- cpuPercent
		}
	}()

	return out, cleanup, nil
}

func runCPUWatch(cmd *cobra.Command, args []string) {

	cpuStream, cleanup, err := StartCPUCollector()
	if err != nil {
		log.Fatalf("CPU collector init error: %v", err)
	}
	defer cleanup()

	fmt.Println("Collecting TOTAL CPU usage… CTRL+C to stop")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case v := <-cpuStream:
			fmt.Printf("CPU: %.2f%%\n", v)

		case <-stop:
			fmt.Println("Stopping cpu collector…")
			return
		}
	}
}
