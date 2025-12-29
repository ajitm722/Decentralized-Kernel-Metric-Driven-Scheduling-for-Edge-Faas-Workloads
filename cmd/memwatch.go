package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
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

// memBPF abstracts the eBPF objects
type memBPF struct {
	progBegin *ebpf.Program
	progEnd   *ebpf.Program
	mapStall  *ebpf.Map
	cleanup   func()
}

func loadMemBPF() (*memBPF, error) {
	var objs mem_collectorObjects
	if err := loadMem_collectorObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("loading mem objects: %v", err)
	}
	return &memBPF{
		progBegin: objs.HandleReclaimBegin,
		progEnd:   objs.HandleReclaimEnd,
		mapStall:  objs.MemStallNs,
		cleanup:   func() { objs.Close() },
	}, nil
}

// StartMEMCollector starts the eBPF memory collector and returns a channel with memory pressure %.
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go mem_collector ../bpf/mem_collector.c -- -I../bpf/headers
func StartMEMCollector() (<-chan float64, func(), error) {

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	bpf, err := loadMemBPF()
	if err != nil {
		return nil, nil, err
	}

	hBegin, err := link.Tracepoint("vmscan", "mm_vmscan_direct_reclaim_begin", bpf.progBegin, nil)
	if err != nil {
		bpf.cleanup()
		return nil, nil, fmt.Errorf("attach reclaim_begin: %v", err)
	}

	hEnd, err := link.Tracepoint("vmscan", "mm_vmscan_direct_reclaim_end", bpf.progEnd, nil)
	if err != nil {
		hBegin.Close()
		bpf.cleanup()
		return nil, nil, fmt.Errorf("attach reclaim_end: %v", err)
	}

	cleanup := func() {
		hBegin.Close()
		hEnd.Close()
		bpf.cleanup()
	}

	out := make(chan float64)

	go pollMemStats(out, bpf)

	return out, cleanup, nil
}

func pollMemStats(out chan<- float64, bpf *memBPF) {
	defer close(out)

	var key uint32 = 0
	last := uint64(0)

	interval := 1 * time.Second
	intervalNS := uint64(interval.Nanoseconds())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		var total uint64
		// CRITICAL SECTION: Lookup Global Stall Time
		// Key 0 represents the singleton entry in the BPF Array Map where we aggregate stalls.
		if err := bpf.mapStall.Lookup(&key, &total); err != nil {
			continue
		}

		// Calculate the delta: How many nanoseconds were spent stalled in direct reclaim
		// during this specific polling interval.
		delta := total - last
		last = total

		// Pressure % = (Time Stalled / Total Wall Time) * 100
		memPercent := (float64(delta) / float64(intervalNS)) * 100.0

		out <- memPercent
	}
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
