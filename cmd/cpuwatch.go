package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/spf13/cobra"
)

var cpuwatchCmd = &cobra.Command{
	Use:   "cpuwatch",
	Short: "Collect CPU usage (Auto-Detect Core vs Tegra)",
	Run:   runCPUWatch,
}

func init() {
	rootCmd.AddCommand(cpuwatchCmd)
}

// Generate BOTH binaries with the correct include path for Ubuntu headers.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go cpu_core ../bpf/cpu_core.c -- -O2 -target bpf -I/usr/include/x86_64-linux-gnu
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go cpu_tegra ../bpf/cpu_tegra.c -- -O2 -target bpf -I/usr/include/x86_64-linux-gnu

// cpuBPF abstracts the specific eBPF objects (Tegra vs Core)
type cpuBPF struct {
	progSwitch *ebpf.Program
	progExit   *ebpf.Program
	cpuMap     *ebpf.Map
	cleanup    func()
}

func loadCpuTegraBPF() (*cpuBPF, error) {
	var objs cpu_tegraObjects
	if err := loadCpu_tegraObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("load tegra: %v", err)
	}
	return &cpuBPF{
		progSwitch: objs.HandleSchedSwitch,
		progExit:   objs.HandleProcessExit,
		cpuMap:     objs.CpuUsage,
		cleanup:    func() { objs.Close() },
	}, nil
}

func loadCpuCoreBPF() (*cpuBPF, error) {
	var objs cpu_coreObjects
	if err := loadCpu_coreObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("load core: %v", err)
	}
	return &cpuBPF{
		progSwitch: objs.HandleSchedSwitch,
		progExit:   objs.HandleProcessExit,
		cpuMap:     objs.CpuUsage,
		cleanup:    func() { objs.Close() },
	}, nil
}

func StartCPUCollector() (<-chan float64, func(), error) {
	// necessary to make ebpf code work
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	// 1. Detect Kernel
	release := detectKernel()
	log.Printf("Kernel Detected: %s", release)

	// 2. Load the Right Object
	var loader func() (*cpuBPF, error)
	if strings.Contains(release, "tegra") {
		log.Println("Mode: JETSON TEGRA (Offset 28)")
		loader = loadCpuTegraBPF
	} else {
		log.Println("Mode: STANDARD CORE (Offset 24)")
		loader = loadCpuCoreBPF
	}

	bpf, err := loader()
	if err != nil {
		return nil, nil, err
	}

	// 3. Attach Tracepoints
	hSwitch, err := link.Tracepoint("sched", "sched_switch", bpf.progSwitch, nil)
	if err != nil {
		bpf.cleanup()
		return nil, nil, fmt.Errorf("attach switch: %v", err)
	}

	hExit, err := link.Tracepoint("sched", "sched_process_exit", bpf.progExit, nil)
	if err != nil {
		hSwitch.Close()
		bpf.cleanup()
		return nil, nil, fmt.Errorf("attach exit: %v", err)
	}

	finalCleanup := func() {
		hSwitch.Close()
		hExit.Close()
		bpf.cleanup()
	}

	// 4. Start Polling
	out := make(chan float64)

	go pollCPUStats(out, bpf)

	return out, finalCleanup, nil
}

func pollCPUStats(out chan<- float64, bpf *cpuBPF) {
	numCPUs := float64(runtime.GOMAXPROCS(0))
	if numCPUs < 1 {
		numCPUs = float64(runtime.NumCPU())
	}

	defer close(out)
	lastCPU := make(map[uint32]uint64)
	poll := 1 * time.Second
	intervalNS := uint64(poll.Nanoseconds())
	// CRITICAL: We scale the interval by the number of CPUs because the kernel
	// accounts for CPU time per-core. 1 second of wall time on 4 cores = 4 seconds of CPU time.
	scaledIntervalNS := uint64(float64(intervalNS) * numCPUs)

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for range ticker.C {
		var totalDelta uint64
		var pid uint32
		var ns uint64

		// CRITICAL SECTION: Iterate over the BPF Hash Map
		// This map contains the cumulative CPU time (in ns) for every PID seen.
		iter := bpf.cpuMap.Iterate()
		for iter.Next(&pid, &ns) {
			// Calculate the CPU time consumed by this PID since the last poll.
			// If ns > prev, the process has run during this interval.
			prev := lastCPU[pid]
			if ns > prev {
				totalDelta += ns - prev
			}
			lastCPU[pid] = ns
		}
		// Calculate total CPU usage percentage across all cores.
		out <- (float64(totalDelta) / float64(scaledIntervalNS)) * 100.0
	}
}

func runCPUWatch(cmd *cobra.Command, args []string) {
	stream, cleanup, err := StartCPUCollector()
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()
	fmt.Println("Collecting CPU... CTRL+C to stop")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case v := <-stream:
			fmt.Printf("CPU: %.2f%%\n", v)
		case <-stop:
			return
		}
	}
}
