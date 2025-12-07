package cmd

import (
	"context"
	"fmt"
	"net"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	pb "ebpf_edge/proto"
)

var leaderCmd = &cobra.Command{
	Use:   "leader",
	Short: "Start gRPC server to receive metrics",
	Run:   runLeader,
}

func init() {
	rootCmd.AddCommand(leaderCmd)
}

type server struct {
	pb.UnimplementedMetricsServiceServer
}

func (s *server) Push(ctx context.Context, m *pb.MetricsSnapshot) (*pb.Ack, error) {
	fmt.Printf("[LEADER] CPU=%.2f MEM=%.2f TEMP=%.1f°C (%s) zone=%s\n",
		m.Cpu, m.Mem, m.TempC, m.TempStatus, m.Zone)

	return &pb.Ack{Msg: "OK"}, nil
}

func runLeader(cmd *cobra.Command, args []string) {
	lis, err := net.Listen("tcp", ":60000")
	if err != nil {
		panic(err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterMetricsServiceServer(grpcServer, &server{})

	fmt.Println("Leader listening on :60000 ...")
	if err := grpcServer.Serve(lis); err != nil {
		panic(err)
	}
}
