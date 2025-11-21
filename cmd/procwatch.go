package cmd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/spf13/cobra"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target native procwatch ../bpf/procwatch.c -- -I../bpf/headers

type proc_data_t struct {
	Pid  uint32
	Ppid uint32
	Comm [16]byte
	Argv [256]byte
}

var procwatchCmd = &cobra.Command{
	Use:   "procwatch",
	Short: "Watch new processes as they start",
	Long: `procwatch tails the sched_process_exec tracepoint to surface newly started processes.
It captures the PID, command name, and arguments for quick detection and triage.

Examples:
  ebpf_edge procwatch                    # Monitor all process executions
  ebpf_edge procwatch --pid 1234         # Monitor executions by specific PID
  ebpf_edge procwatch --comm "bash"      # Monitor executions by specific command`,
	Run: runProcwatch,
}

var (
	procPidFilter  uint32
	procCommFilter string
)

func init() {
	procwatchCmd.Flags().Uint32Var(&procPidFilter, "pid", 0, "Filter by process ID")
	procwatchCmd.Flags().StringVar(&procCommFilter, "comm", "", "Filter by command name")
	rootCmd.AddCommand(procwatchCmd)
}

func runProcwatch(cmd *cobra.Command, args []string) {
	// Subscribe to signals for terminating the program.
	stopper := make(chan os.Signal, 1)
	signal.Notify(stopper, os.Interrupt, syscall.SIGTERM)

	// Allow the current process to lock memory for eBPF resources.
	if err := rlimit.RemoveMemlock(); err != nil {
		log.Fatal(err)
	}

	// Load pre-compiled programs and maps into the kernel.
	objs := procwatchObjects{}
	if err := loadProcwatchObjects(&objs, nil); err != nil {
		log.Fatalf("loading objects: %v", err)
	}
	defer objs.Close()

	// Attach to tracepoints
	tpEnterLink, err := link.Tracepoint("sched", "sched_process_exec", objs.TraceExec, nil)
	if err != nil {
		log.Fatalf("Failed to attach tracepoint: %s", err)
	}
	defer tpEnterLink.Close()

	fmt.Println("Monitoring process executions... Press Ctrl+C to stop")
	fmt.Println("PID\tCommand\t\tArguments")
	fmt.Println("---\t-------\t\t----------")

	// Initialize ring buffer
	events := objs.Events
	rd, err := ringbuf.NewReader(events)
	if err != nil {
		log.Fatalf("Failed to create ringbuf reader: %s", err)
	}
	defer rd.Close()

	// Handle incoming events
	go func() {
		for {
			record, err := rd.Read()
			if err != nil {
				if errors.Is(err, ringbuf.ErrClosed) {
					return
				}
				log.Printf("Error reading from buffer: %s", err)
				continue
			}

			var data proc_data_t
			if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &data); err != nil {
				log.Printf("Error decoding event: %s", err)
				continue
			}

			comm := string(bytes.Trim(data.Comm[:], "\x00"))
			argv := string(bytes.Trim(data.Argv[:], "\x00"))

			// Apply filters
			if procPidFilter != 0 && data.Pid != procPidFilter {
				continue
			}
			if procCommFilter != "" && comm != procCommFilter {
				continue
			}

			fmt.Printf("%d\t%s\t\t%s\n", data.Pid, comm, argv)
		}
	}()

	// Wait for interrupt
	<-stopper
	fmt.Println("\nStopping process execution monitor...")
}
