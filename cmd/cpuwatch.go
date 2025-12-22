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

func StartCPUCollector() (<-chan float64, func(), error) {

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, nil, fmt.Errorf("rlimit error: %v", err)
	}

	var (
		hSwitch link.Link
		hExit   link.Link
		cpuMap  *ebpf.Map
		cleanup func()
		progS   *ebpf.Program
		progE   *ebpf.Program
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
	log.Printf("Kernel Detected: %s", release)

	// 2. Load the Right Object
	if strings.Contains(release, "tegra") {
		log.Println("Mode: JETSON TEGRA (Offset 28)")
		var objs cpu_tegraObjects
		if err := loadCpu_tegraObjects(&objs, nil); err != nil {
			return nil, nil, fmt.Errorf("load tegra: %v", err)
		}
		progS = objs.HandleSchedSwitch
		progE = objs.HandleProcessExit
		cpuMap = objs.CpuUsage
		cleanup = func() { objs.Close() }
	} else {
		log.Println("Mode: STANDARD CORE (Offset 24)")
		var objs cpu_coreObjects
		if err := loadCpu_coreObjects(&objs, nil); err != nil {
			return nil, nil, fmt.Errorf("load core: %v", err)
		}
		progS = objs.HandleSchedSwitch
		progE = objs.HandleProcessExit
		cpuMap = objs.CpuUsage
		cleanup = func() { objs.Close() }
	}

	// 3. Attach Tracepoints
	var err error
	hSwitch, err = link.Tracepoint("sched", "sched_switch", progS, nil)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("attach switch: %v", err)
	}

	hExit, err = link.Tracepoint("sched", "sched_process_exit", progE, nil)
	if err != nil {
		hSwitch.Close()
		cleanup()
		return nil, nil, fmt.Errorf("attach exit: %v", err)
	}

	finalCleanup := func() {
		hSwitch.Close()
		hExit.Close()
		cleanup()
	}

	// 4. Start Polling
	out := make(chan float64)
	numCPUs := float64(runtime.GOMAXPROCS(0))
	if numCPUs < 1 {
		numCPUs = float64(runtime.NumCPU())
	}

	go func() {
		defer close(out)
		lastCPU := make(map[uint32]uint64)
		poll := 500 * time.Millisecond
		intervalNS := uint64(poll.Nanoseconds())
		scaledIntervalNS := uint64(float64(intervalNS) * numCPUs)

		ticker := time.NewTicker(poll)
		defer ticker.Stop()

		for range ticker.C {
			var totalDelta uint64
			var pid uint32
			var ns uint64

			iter := cpuMap.Iterate()
			for iter.Next(&pid, &ns) {
				prev := lastCPU[pid]
				if ns > prev {
					totalDelta += ns - prev
				}
				lastCPU[pid] = ns
			}
			out <- (float64(totalDelta) / float64(scaledIntervalNS)) * 100.0
		}
	}()

	return out, finalCleanup, nil
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
