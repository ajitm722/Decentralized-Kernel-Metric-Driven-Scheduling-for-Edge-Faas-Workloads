package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	pb "ebpf_edge/proto"
)

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

const (
	// DaemonLocalAddr is the address of the local peer node we submit jobs to.
	DaemonLocalAddr = "localhost:60000"

	// JobSubmissionTimeout defines how long the CLI waits for the Daemon to accept the job.
	JobSubmissionTimeout = 5 * time.Minute
)

var runCmd = &cobra.Command{
	Use:   "run [workload_type]",
	Short: "Submit a workload (IMG_RESIZE, DATA_ETL, MATRIX_OPS)",
	Args:  cobra.ExactArgs(1),
	Run:   runJob,
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func runJob(cmd *cobra.Command, args []string) {
	taskType := args[0]
	fmt.Printf("Submitting Task: %s\n", taskType)

	// 1. Define Workload Profiles
	// RESEARCH NOTE:
	// For the purpose of this research, we map high-level "Business" workload names
	// to low-level synthetic stress tests using 'stress-ng'.
	// This allows us to predictably stimulate specific kernel subsystems (CPU vs VM vs IO)
	// to validate the scheduler's behavior under pressure.
	var req *pb.JobRequest

	switch taskType {
	case "IMG_RESIZE":
		// SIMULATION: Image Resizing
		// Characteristics: High CPU usage (integer/float math), low memory footprint.
		req = &pb.JobRequest{
			Name:   "IMG_RESIZE",
			ReqCpu: 70.0,
			ReqMem: 10.0,
			Image:  "alexeiled/stress-ng",
			// Args: Spawn 2 CPU stressors for 30 seconds
			Args: []string{"--cpu", "2", "--timeout", "30s"},
			Id:   uuid.New().String(),
		}

	case "DATA_ETL":
		// SIMULATION: Extract-Transform-Load (Data Processing)
		// Characteristics: High Memory usage (large buffer allocation), Moderate CPU.
		req = &pb.JobRequest{
			Name:   "DATA_ETL",
			ReqCpu: 10.0,
			ReqMem: 30.0,
			Image:  "alexeiled/stress-ng",
			// Args: Spawn 2 VM workers consuming 128MB each for 30 seconds
			Args: []string{"--vm", "2", "--vm-bytes", "128M", "--timeout", "30s"},
			Id:   uuid.New().String(),
		}

	case "MATRIX_OPS":
		// SIMULATION: Machine Learning / Matrix Multiplication
		// Characteristics: Intensive floating point math and CPU cache thrashing.
		req = &pb.JobRequest{
			Name:   "MATRIX_OPS",
			ReqCpu: 40.0,
			ReqMem: 15.0,
			Image:  "alexeiled/stress-ng",
			// Args: Spawn 1 Matrix stressor to simulate dense computation
			Args: []string{"--matrix", "1", "--timeout", "30s"},
			Id:   uuid.New().String(),
		}

	default:
		fmt.Println("Unknown workload. Available: IMG_RESIZE, DATA_ETL, MATRIX_OPS")
		return
	}

	// 2. Connect
	conn, err := grpc.Dial(DaemonLocalAddr, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		return
	}
	defer conn.Close()

	client := pb.NewMetricsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), JobSubmissionTimeout)
	defer cancel()

	// 3. Submit
	fmt.Println(">> Submitting Job... (Waiting for execution completion)")
	resp, err := client.SubmitJob(ctx, req)
	if err != nil {
		fmt.Printf(">> Job Failed: %v\n", err)
		return
	}

	fmt.Printf(">> Result: %s\n", resp.Msg)
	fmt.Printf(">> Executed by Node: %s\n", resp.ForwardedTo)
}
