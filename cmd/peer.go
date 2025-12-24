package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	pb "ebpf_edge/proto"
)

// NodeTTL determines how long we keep a node "Online" before marking it stale.
const NodeTTL = 10 * time.Second

var targetPeers []string

var peerCmd = &cobra.Command{
	Use:   "peer",
	Short: "Start P2P Mesh: Collect local metrics and share with peers",
	Run:   runPeer,
}

func init() {
	rootCmd.AddCommand(peerCmd)
	peerCmd.Flags().StringSliceVar(&targetPeers, "peers", []string{}, "Comma-separated list of peer IPs")
}

// -----------------------------------------------------------------------------
// Cluster State Management
// -----------------------------------------------------------------------------

// NodeData wraps the raw metrics with a timestamp so we know if they are fresh.
type NodeData struct {
	Snapshot *pb.MetricsSnapshot
	LastSeen time.Time
}

type ClusterState struct {
	mu      sync.RWMutex
	Metrics map[string]NodeData // Changed from *pb.MetricsSnapshot to NodeData
}

var globalCluster = ClusterState{
	Metrics: make(map[string]NodeData),
}

// Update saves the metrics and updates the "LastSeen" timestamp.
func (c *ClusterState) Update(ip string, snap *pb.MetricsSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Metrics[ip] = NodeData{
		Snapshot: snap,
		LastSeen: time.Now(), // <--- Reset the timer
	}
}

// Snapshot returns a copy.
// We return the full NodeData so the display loop can calculate the age itself.
func (c *ClusterState) Snapshot() map[string]NodeData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	copyState := make(map[string]NodeData)
	for k, v := range c.Metrics {
		copyState[k] = v
	}
	return copyState
}

// -----------------------------------------------------------------------------
// gRPC Server (Ingress)
// -----------------------------------------------------------------------------

type peerServer struct {
	pb.UnimplementedMetricsServiceServer
}

func (s *peerServer) Push(ctx context.Context, m *pb.MetricsSnapshot) (*pb.Ack, error) {
	p, ok := peer.FromContext(ctx)
	senderIP := "unknown"
	if ok {
		host, _, err := net.SplitHostPort(p.Addr.String())
		if err == nil {
			senderIP = host
		}
	}
	globalCluster.Update(senderIP, m)
	return &pb.Ack{Msg: "OK"}, nil
}

func startServer(port string) {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Printf("Error binding to port %s: %v\n", port, err)
		return
	}
	grpcServer := grpc.NewServer()
	pb.RegisterMetricsServiceServer(grpcServer, &peerServer{})

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			fmt.Printf("gRPC server error: %v\n", err)
		}
	}()
}

// -----------------------------------------------------------------------------
// Main Application Logic
// -----------------------------------------------------------------------------

func runPeer(cmd *cobra.Command, args []string) {
	startServer("60000")

	cpuStream, cpuCleanup, err := StartCPUCollector()
	if err != nil {
		fmt.Println("CPU collector init failed:", err)
		return
	}
	defer cpuCleanup()

	memStream, memCleanup, err := StartMEMCollector()
	if err != nil {
		fmt.Println("MEM collector init failed:", err)
		return
	}
	defer memCleanup()

	tempStream, tempCleanup, err := StartTEMPCollector()
	if err != nil {
		fmt.Println("TEMP collector init failed:", err)
		return
	}
	defer tempCleanup()

	var localSnap MetricsSnapshot

	go func() {
		for v := range cpuStream {
			localSnap.UpdateCPU(v)
		}
	}()
	go func() {
		for v := range memStream {
			localSnap.UpdateMem(v)
		}
	}()
	go func() {
		for v := range tempStream {
			localSnap.UpdateTemp(v)
		}
	}()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("------------------------------------------------")
	fmt.Printf("Node Started. Gossip Targets: %v\n", targetPeers)
	fmt.Println("Press CTRL+C to stop.")
	fmt.Println("------------------------------------------------")

	for {
		select {
		case <-ticker.C:
			current := localSnap.Read()
			protoData := &pb.MetricsSnapshot{
				Cpu:        current.CPUPercent,
				Mem:        current.MemPercent,
				TempC:      current.TempC,
				TempStatus: current.TempStatus,
				Zone:       current.ZoneName,
			}

			// Update localhost (so we don't time ourselves out)
			globalCluster.Update("localhost", protoData)

			broadcastMetrics(targetPeers, protoData)
			displayCluster()

		case <-stop:
			fmt.Println("\nShutting down peer node...")
			return
		}
	}
}

// -----------------------------------------------------------------------------
// Client / Gossip Logic (Egress)
// -----------------------------------------------------------------------------

func broadcastMetrics(peers []string, data *pb.MetricsSnapshot) {
	if len(peers) == 0 {
		return
	}
	for _, ip := range peers {
		if strings.TrimSpace(ip) == "" {
			continue
		}
		go sendToPeer(ip, data)
	}
}

func sendToPeer(ip string, data *pb.MetricsSnapshot) {
	target := ip + ":60000"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, target, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return
	}
	defer conn.Close()

	client := pb.NewMetricsServiceClient(conn)
	_, _ = client.Push(ctx, data)
}

// -----------------------------------------------------------------------------
// Display Logic
// -----------------------------------------------------------------------------

func displayCluster() {
	view := globalCluster.Snapshot()

	// Clear screen
	fmt.Print("\033[H\033[2J")

	// Header
	fmt.Println("=============================================================================")
	fmt.Printf("   DECENTRALIZED METRICS MESH (Nodes: %d)\n", len(view))
	fmt.Println("=============================================================================")
	// I increased the TEMP column width to 15 to fit "(SAFE)" cleanly
	fmt.Printf("%-16s | %-8s | %-8s | %-15s | %-10s\n", "IP ADDRESS", "CPU", "MEM", "TEMP", "STATUS")
	fmt.Println("-----------------------------------------------------------------------------")

	// Sort IPs for stable order
	var ips []string
	for ip := range view {
		ips = append(ips, ip)
	}
	sort.Strings(ips)

	for _, ip := range ips {
		data := view[ip]
		m := data.Snapshot

		// 1. Calculate Age & Online Status
		age := time.Since(data.LastSeen)
		var statusStr string

		if age > NodeTTL {
			// Node is stale/offline
			statusStr = fmt.Sprintf("\033[31mOFFLINE\033[0m (%.0fs)", age.Seconds())

			// Display row with dashes for metrics
			fmt.Printf("%-16s | %-8s | %-8s | %-15s | %s\n",
				ip, "-", "-", "-", statusStr)
			continue
		}

		// Node is Online
		statusStr = "\033[32mONLINE\033[0m"

		// 2. Format Temperature
		// If TempStatus is empty, it means the collector never sent data (eBPF inactive/no sensor)
		var tempStr string
		if m.TempStatus == "" {
			tempStr = "N/A" // Explicitly show unavailable
		} else {
			tempStr = fmt.Sprintf("%.1f°C (%s)", m.TempC, m.TempStatus)
		}

		// 3. Print Row
		fmt.Printf("%-16s | %6.1f%% | %6.1f%% | %-15s | %s\n",
			ip,
			m.Cpu,
			m.Mem,
			tempStr,
			statusStr,
		)
	}
	fmt.Println("=============================================================================")
}
