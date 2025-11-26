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

var memwatchCmd = &cobra.Command{
	Use:   "memwatch",
	Short: "Collect memory pressure via direct reclaim stall time",
	Run:   runMemWatch,
}

func init() {
	rootCmd.AddCommand(memwatchCmd)
}

// StartMEMCollector starts the eBPF memory collector and returns a channel with memory pressure %.
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go mem_collector ../bpf/mem_collector.c -- -I../bpf/headers
func StartMEMCollector() (<-chan float64, func(), error) {

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	var objs mem_collectorObjects
	if err := loadMem_collectorObjects(&objs, nil); err != nil {
		return nil, nil, fmt.Errorf("loading mem objects: %v", err)
	}

	hBegin, err := link.Tracepoint("vmscan", "mm_vmscan_direct_reclaim_begin", objs.HandleReclaimBegin, nil)
	if err != nil {
		objs.Close()
		return nil, nil, fmt.Errorf("attach reclaim_begin: %v", err)
	}

	hEnd, err := link.Tracepoint("vmscan", "mm_vmscan_direct_reclaim_end", objs.HandleReclaimEnd, nil)
	if err != nil {
		hBegin.Close()
		objs.Close()
		return nil, nil, fmt.Errorf("attach reclaim_end: %v", err)
	}

	cleanup := func() {
		hBegin.Close()
		hEnd.Close()
		objs.Close()
	}

	out := make(chan float64)

	go func() {
		defer close(out)

		var key uint32 = 0
		last := uint64(0)

		interval := 500 * time.Millisecond
		intervalNS := uint64(interval.Nanoseconds())

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {

			var total uint64
			if err := objs.MemStallNs.Lookup(&key, &total); err != nil {
				continue
			}

			delta := total - last
			last = total

			memPercent := (float64(delta) / float64(intervalNS)) * 100.0

			out <- memPercent
		}
	}()

	return out, cleanup, nil
}

func runMemWatch(cmd *cobra.Command, args []string) {

	memStream, cleanup, err := StartMEMCollector()
	if err != nil {
		log.Fatalf("memory collector init error: %v", err)
	}
	defer cleanup()

	fmt.Println("Collecting MEMORY pressure… CTRL+C to stop")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case v := <-memStream:
			fmt.Printf("MEM: %.2f%%\n", v)

		case <-stop:
			fmt.Println("Stopping mem collector…")
			return
		}
	}
}
