package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	pb "ebpf_edge/proto"
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
func runAggregate(cmd *cobra.Command, args []string) {

	// ----------------------------------------------------
	// Connect to leader (HOST MACHINE)
	// ----------------------------------------------------
	conn, err := grpc.Dial("192.168.0.250:60000", grpc.WithInsecure())
	// 10.0.2.2 = host machine from inside QEMU
	if err != nil {
		fmt.Println("Failed to connect to leader:", err)
		return
	}
	defer conn.Close()

	client := pb.NewMetricsServiceClient(conn)

	// Shared snapshot
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
	// Main output + sending loop
	// ----------------------------------------------------
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	fmt.Println("Running aggregator... CTRL+C to stop")

	for {
		select {
		case <-ticker.C:
			s := snap.Read()

			// send snapshot to leader
			sendToLeader(client, s)

			// optional: also display on console
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

// ----------------------------------------------------
// Helper: Send snapshot to leader via gRPC
// ----------------------------------------------------
func sendToLeader(client pb.MetricsServiceClient, s MetricsSnapshot) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := client.Push(ctx, &pb.MetricsSnapshot{
		Cpu:        s.CPUPercent,
		Mem:        s.MemPercent,
		TempC:      s.TempC,
		TempStatus: s.TempStatus,
		Zone:       s.ZoneName,
	})

	if err != nil {
		fmt.Println("RPC error:", err)
	}
}
