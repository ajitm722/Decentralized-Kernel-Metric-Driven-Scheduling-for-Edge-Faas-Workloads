package cmd

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	pb "ebpf_edge/proto"
)

// -----------------------------------------------------------------------------
// Constants & Configuration
// -----------------------------------------------------------------------------

const (
	// NodeTTL defines the maximum silence duration before a node is considered stale/offline.
	NodeTTL = 4 * time.Second

	// PeerPort is the TCP port used for gRPC communication between nodes.
	PeerPort = "60000"

	// JobForwardTimeout defines how long we wait when forwarding a job to another peer.
	JobForwardTimeout = 5 * time.Minute

	// DockerShortIDLength is the standard length for displaying Docker container IDs.
	DockerShortIDLength = 12
)

var targetPeers []string

var peerCmd = &cobra.Command{
	Use:   "peer",
	Short: "Start P2P Mesh: Collect, Share, and Schedule",
	Run:   runPeer,
}

func init() {
	rootCmd.AddCommand(peerCmd)
	peerCmd.Flags().StringSliceVar(&targetPeers, "peers", []string{}, "Comma-separated list of peer IPs")
}

// -----------------------------------------------------------------------------
// Cluster State
// -----------------------------------------------------------------------------

type NodeData struct {
	Snapshot *pb.MetricsSnapshot
	LastSeen time.Time
}

type ClusterState struct {
	mu      sync.RWMutex
	Metrics map[string]NodeData
}

var globalCluster = ClusterState{
	Metrics: make(map[string]NodeData),
}

func (c *ClusterState) Update(ip string, snap *pb.MetricsSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Metrics[ip] = NodeData{
		Snapshot: snap,
		LastSeen: time.Now(),
	}
}

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
// Scheduler Logic
// -----------------------------------------------------------------------------

func scheduleJob(job *pb.JobRequest) (string, error) {
	// 1. Get current cluster view
	view := globalCluster.Snapshot()

	fmt.Printf("[SCHEDULER] Assessing candidates for %s (Req: %.1f CPU, %.1f MEM)\n", job.Name, job.ReqCpu, job.ReqMem)

	var validCandidates []string
	var safeCandidates []string

	// 3. Filter Candidates based on Capacity
	for ip, data := range view {
		// A. Check Liveness
		if time.Since(data.LastSeen) > NodeTTL {
			continue
		}

		m := data.Snapshot

		// B. Check Resource Capacity
		// We ensure the node has enough headroom for the request.
		// Thresholds: Max 95% CPU, Max 90% MEM.
		cpuOk := (m.Cpu + job.ReqCpu) < 95.0
		memOk := (m.Mem + job.ReqMem) < 90.0

		if cpuOk && memOk {
			validCandidates = append(validCandidates, ip)

			// --- FIX START: Handle Empty Temp ---
			displayTemp := m.TempStatus
			if displayTemp == "" {
				displayTemp = "N/A"
			}

			// C. Check Thermal Safety
			// Policy: If Status is SAFE *OR* N/A (x86 machines sometimes give invalid value), we consider it safe for now.
			if m.TempStatus == "SAFE" || m.TempStatus == "" {
				safeCandidates = append(safeCandidates, ip)
			}

			fmt.Printf(" -> Candidate Found: %s | CPU: %.1f%% | Temp: %s\n", ip, m.Cpu, displayTemp)
		}
	}

	if len(validCandidates) == 0 {
		return "", fmt.Errorf("no suitable nodes found for job %s (Cluster Overloaded)", job.Name)
	}

	// 4. Selection Strategy: Thermal Priority
	// If we have nodes that are thermally SAFE, we discard any WARM nodes from consideration.
	// If no SAFE nodes exist, we fall back to the generic valid list (WARM but with capacity).
	finalPool := validCandidates
	if len(safeCandidates) > 0 {
		finalPool = safeCandidates
	}

	// 5. Optimization & Selection
	selectedIP := finalPool[rand.Intn(len(finalPool))]

	// 5. Selection Strategy: Latency Awareness (Localhost Priority)
	// If the machine we are currently running on (localhost) is in the final suitable pool,
	// we pick it immediately. This avoids network serialization/deserialization latency.
	// Prioritize Localhost if valid
	for _, ip := range finalPool {
		if ip == "localhost" { // or check myIP
			selectedIP = "localhost"
			break
		}
	}

	// 6. Execution (BLOCKING NOW)
	if selectedIP == "localhost" {
		fmt.Println(" -> Executing locally (Blocking)...")
		err := executeDockerContainer(job) // Wait for finish
		if err != nil {
			return "localhost", err
		}
		return "localhost", nil
	}
	// Forward and WAIT for peer response
	return forwardJobToPeer(selectedIP, job)

}

// CHANGE 2: executeDockerContainer blocks and returns error
func executeDockerContainer(job *pb.JobRequest) error {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client fail: %v", err)
	}
	defer cli.Close()

	// 1. Pull
	reader, err := cli.ImagePull(ctx, job.Image, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull fail: %v", err)
	}
	io.Copy(io.Discard, reader)

	// 2. Create
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: job.Image,
		Cmd:   job.Args,
	}, nil, nil, nil, "")
	if err != nil {
		return fmt.Errorf("create fail: %v", err)
	}

	// 3. Start
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start fail: %v", err)
	}

	fmt.Printf("[DOCKER] Container %s running... waiting for completion.\n", resp.ID[:12])

	// 4. WAIT (Blocking)
	statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return fmt.Errorf("wait error: %v", err)
	case <-statusCh:
		// Success!
		fmt.Printf("[DOCKER] Container %s finished.\n", resp.ID[:DockerShortIDLength])
		return nil
	}
}

// CHANGE 3: forwardJobToPeer blocks and returns the actual node IP
func forwardJobToPeer(ip string, job *pb.JobRequest) (string, error) {
	fmt.Printf("[SCHEDULER] Forwarding Job %s to %s (Waiting)...\n", job.Id, ip)
	target := ip + ":" + PeerPort

	// Long timeout to allow execution
	ctx, cancel := context.WithTimeout(context.Background(), JobForwardTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, target, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return "", fmt.Errorf("dial fail: %v", err)
	}
	defer conn.Close()

	client := pb.NewMetricsServiceClient(conn)

	// This call will now hang until the remote peer finishes the Docker task
	resp, err := client.SubmitJob(ctx, job)
	if err != nil {
		return "", fmt.Errorf("remote exec fail: %v", err)
	}

	// If the remote node says "localhost", it means *that* node ran it.
	// We translate "localhost" -> "IP of Peer" for clarity.
	actualRunner := resp.ForwardedTo
	if actualRunner == "localhost" {
		actualRunner = ip
	}

	return actualRunner, nil
}

// -----------------------------------------------------------------------------
// gRPC Server
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

// CHANGE 4: RPC Handler passes the return values back
func (s *peerServer) SubmitJob(ctx context.Context, job *pb.JobRequest) (*pb.Ack, error) {
	fmt.Printf("\n[RPC] Received Job Request: %s\n", job.Name)

	target, err := scheduleJob(job)
	if err != nil {
		return &pb.Ack{Msg: "Failed", ForwardedTo: ""}, err
	}

	return &pb.Ack{Msg: "Completed Successfully", ForwardedTo: target}, nil
}

func startServer(port string) {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Printf("Error binding port %s: %v\n", port, err)
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
// Main Loop
// -----------------------------------------------------------------------------

func runPeer(cmd *cobra.Command, args []string) {
	startServer(PeerPort)

	// Initialize Collectors
	cpuStream, cpuCleanup, err := StartCPUCollector()
	if err != nil {
		fmt.Println("CPU init failed:", err)
		return
	}
	defer cpuCleanup()

	memStream, memCleanup, err := StartMEMCollector()
	if err != nil {
		fmt.Println("MEM init failed:", err)
		return
	}
	defer memCleanup()

	tempStream, tempCleanup, err := StartTEMPCollector()
	if err != nil {
		fmt.Println("TEMP init failed:", err)
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

	fmt.Printf("Node Started. Gossip Targets: %v\n", targetPeers)

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

			globalCluster.Update("localhost", protoData)
			broadcastMetrics(targetPeers, protoData)
			displayCluster()

		case <-stop:
			fmt.Println("\nShutting down...")
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
	// HEADER: Use %-10s for CPU/MEM to match the data rows below
	fmt.Printf("%-16s | %-10s | %-10s | %-15s | %-10s\n", "IP ADDRESS", "CPU", "MEM", "TEMP", "STATUS")
	fmt.Println("-----------------------------------------------------------------------------------")
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

			// OFFLINE ROW: Use %-10s to match header width
			fmt.Printf("%-16s | %-10s | %-10s | %-15s | %s\n",
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
		// ONLINE ROW: Use %9.1f%% -> 9 chars for number + 1 char for '%' = 10 chars Total
		fmt.Printf("%-16s | %9.1f%% | %9.1f%% | %-15s | %s\n",
			ip,
			m.Cpu,
			m.Mem,
			tempStr,
			statusStr,
		)
	}
	fmt.Println("=============================================================================")
}
