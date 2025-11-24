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

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target native mem_collector ../bpf/mem_collector.c -- -I../bpf/headers

func runMemWatch(cmd *cobra.Command, args []string) {

	if err := rlimit.RemoveMemlock(); err != nil {
		log.Fatalf("rlimit error: %v", err)
	}

	var objs mem_collectorObjects
	if err := loadMem_collectorObjects(&objs, nil); err != nil {
		log.Fatalf("loading objects: %v", err)
	}
	defer objs.Close()

	// Attach begin
	hBegin, err := link.Tracepoint("vmscan", "mm_vmscan_direct_reclaim_begin", objs.HandleReclaimBegin, nil)
	if err != nil {
		log.Fatalf("Failed to attach reclaim_begin: %v", err)
	}
	defer hBegin.Close()

	// Attach end
	hEnd, err := link.Tracepoint("vmscan", "mm_vmscan_direct_reclaim_end", objs.HandleReclaimEnd, nil)
	if err != nil {
		log.Fatalf("Failed to attach reclaim_end: %v", err)
	}
	defer hEnd.Close()

	fmt.Println("Collecting MEMORY pressure... CTRL+C to stop")

	var key uint32 = 0
	last := uint64(0)

	interval := 500 * time.Millisecond
	intervalNS := uint64(interval.Nanoseconds())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:

			var total uint64
			if err := objs.MemStallNs.Lookup(&key, &total); err != nil {
				continue
			}

			delta := total - last
			last = total

			memPercent := (float64(delta) / float64(intervalNS)) * 100.0

			fmt.Printf("MEM: %.2f%%\n", memPercent)

		case <-stop:
			fmt.Println("Stopping mem collector...")
			return
		}
	}
}
