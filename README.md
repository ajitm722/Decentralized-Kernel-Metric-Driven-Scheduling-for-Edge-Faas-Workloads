# Decentralized Kernel-Metric-Driven Scheduling for Edge FaaS Workloads

### Overview

This project implements a leaderless, decentralized Function-as-a-Service (FaaS) framework designed to prevent resource exhaustion in constrained edge clusters. By leveraging low-level kernel metrics, the system detects localized saturation and proactively offloads workloads to healthy peer nodes, ensuring continuous operation without central coordination.

### The Real-World Problem

In edge environments like precision agriculture or industrial IoT, devices such as the NVIDIA Jetson Orin Nano often operate near their thermal and compute limits. While these devices can easily handle routine telemetry, they are vulnerable to **bursty anomalies**.

Consider a real-world scenario:

1. **Primary Event:** An Orin Nano is performing heavy image recognition to classify a defect on an assembly line. This spikes the GPU and CPU, raising the device temperature.
2. **Simultaneous Event:** A second anomaly occurs—such as a sudden vibration spike requiring immediate signal analysis or a control-loop reconfiguration.
3. **The Failure Mode:** In a static setup, the Nano attempts to process both. The resulting thermal load forces the hardware to throttle, causing missed deadlines (e.g., failing to eject the defective part) or, in extreme cases, risking hardware damage due to sustained overheating.

The core thesis of this project is that **no single node should be allowed to fail alone while neighbors sit idle.** Instead of queuing the second task on the already saturated Nano, the system automatically detects the rising thermal/CPU pressure and offloads the containerized task to a neighboring device in the cluster that has spare capacity.

### The Solution Architecture

To achieve this responsiveness without the overhead of a heavy cloud orchestrator (like Kubernetes), this framework employs two specific architectural decisions:

* **Metric-Driven Offloading (via eBPF):**
Standard CPU metrics are often too slow to catch rapid load spikes. This project uses **Extended Berkeley Packet Filter (eBPF)** to read high-fidelity scheduling and thermal data directly from the kernel. This allows the node to "realize" it is becoming overworked milliseconds before a critical failure occurs.
* **Leaderless Coordination (via P2P Gossip):**
Traditional clusters rely on a master node to make scheduling decisions. If that master fails or the network partitions, the system halts. This framework uses a **Peer-to-Peer gossip protocol**, allowing every node to independently understand the cluster's health. If a node needs help, it finds a peer directly, eliminating the single point of failure.

### Key Results

Experimental evaluation on heterogeneous ARM64 clusters demonstrates that this approach can successfully prevent thermal throttling and maintains system responsiveness during simultaneous anomaly events.

## Core Features

* **Fully Decentralized & Leaderless**
    The system operates without a central control plane or leader election. Nodes coordinate exclusively via a lightweight peer-to-peer gossip protocol, ensuring the cluster remains functional even if multiple nodes fail or the network partitions.

* **Hybrid Observability Plane**
    The system employs a multi-layered approach to monitoring node health:
    * **CPU & Thermal (eBPF):** Uses custom eBPF tracepoints to capture high-frequency scheduling events and thermal spikes directly from the kernel with minimal overhead.
    * **Memory (Polling):** Utilizes standard `/proc/meminfo` parsing to track capacity-based memory pressure.
    This hybrid design balances precision with implementation complexity, ensuring that rapidly changing signals (CPU/Thermal) are traced in real-time, while stable resources (RAM) are monitored via standard interfaces.

* **Proactive Load Shedding**
    The scheduler uses a metric-driven approach to detect execution risks before they result in failure. When a node approaches its thermal or compute limits, it transparently offloads containerized functions to a healthier peer, preserving the stability of the saturated device.

* **Sustainable Resource Usage**
    Designed to extend the lifecycle of small-form-factor devices (SBCs), the framework allows heterogeneous hardware to form a unified compute pool. This demonstrates that decentralized orchestration is a viable, low-energy alternative to centralized cloud platforms for specific edge workloads.

## Background: eBPF Architecture

This project leverages **Extended Berkeley Packet Filter (eBPF)** to achieve nanosecond-precision observability. eBPF is a kernel technology that allows developers to execute custom logic within the operating system kernel without the risk or complexity of writing standard kernel modules.

* **Sandboxed & Verified Safety**
    Traditionally, kernel modules run with absolute privilege, meaning a single bug can panic the entire OS. eBPF solves this via a rigid **In-Kernel Verifier**. Before any program is loaded, the verifier statically analyzes the bytecode to guarantee memory safety, enforce strict control flow limits (preventing infinite loops), and ensure the program terminates predictably.

* **Event-Driven Execution Model**
    eBPF programs are not background processes; they are event handlers. They attach to specific **Hooks**—such as tracepoints, system calls, or network events. The code remains dormant until the specific event triggers it, ensuring that resources are consumed only when relevant activity occurs (e.g., a process context switch).

* **JIT Compilation & Native Performance**
    While distributed as architecture-independent bytecode, eBPF programs are **Just-In-Time (JIT) compiled** into native machine instructions immediately after verification. This allows the instrumentation to run with the performance characteristics of native kernel code, avoiding the overhead of an interpreter.

* **Data Exchange via Maps**
    To bridge the gap between kernel-space and user-space, eBPF uses **Maps**—efficient, kernel-resident data structures (e.g., Hash Tables, Ring Buffers). These allow the eBPF program to store metrics (like CPU timing deltas) which the user-space agent can asynchronously poll or consume, decoupling data collection from data processing.

* **Stable Helper API**
    For security and stability, eBPF programs cannot call arbitrary kernel functions. Instead, they utilize a fixed set of **Helper Functions** provided by the kernel. These helpers allow controlled access to system data (such as current time, process IDs, or packet data) while insulating the program from changes in internal kernel implementation details.

* **Toolchain & Loading**
    Programs are typically written in restricted C and compiled into bytecode using the **LLVM/Clang** toolchain. They are then loaded into the kernel via the `bpf()` system call. In this project, the Go-based agent handles the lifecycle management (loading, attaching, and reading) of these programs automatically.

## The Observability Plane: Implementation Details

This project implements a custom observability plane that extracts CPU, thermal, and memory signals directly from the kernel. Unlike standard monitoring tools (like `top` or `htop`) that "sample" the CPU at fixed intervals—often missing rapid micro-bursts—this system traces the actual scheduler behavior to guarantee **slice-accurate attribution**.

### Precision CPU Tracking via Context Switches
To calculate CPU usage, the eBPF program attaches to the `sched_switch` tracepoint. This event fires every single time the OS scheduler swaps one task for another. By treating this event as a trigger, the program effectively acts as a nanosecond-precision "stopwatch" for every process on the system.

![CPU Tracking Algorithm](assets/alg1_cpu_ebpf.png)

The logic follows a strict event-driven flow:

1.  **Capture the Switch:** The eBPF program wakes up immediately when the scheduler replaces `prev_pid` (the task stopping) with `next_pid` (the task starting).
2.  **Calculate Delta (The "Stopwatch" Stop):** It retrieves the timestamp when `prev_pid` *started* running (stored in an eBPF Map) and subtracts it from the current time (`now - start_time`). This delta is added to the process's cumulative runtime.
3.  **Reset Timer (The "Stopwatch" Start):** It records the current timestamp for `next_pid`, marking the exact start of its execution slice.
4.  **Cleanup:** A separate hook on `sched_process_exit` ensures that when a process terminates, its entries in the maps are immediately deleted to prevent memory leaks.

This approach ensures **O(1) complexity**—constant time lookups regardless of system load—allowing the agent to monitor high-frequency scheduling without degrading performance.

### Handling Heterogeneity: Standard vs. Tegra Kernels
A major challenge in edge computing is hardware variance. Even when running "Linux," vendor-specific kernels often modify internal data structures (ABIs).

During development on the **NVIDIA Jetson Orin Nano**, we identified a critical divergence in the `sched_switch` tracepoint layout compared to standard Raspberry Pi (Debian) kernels:

* **Standard Linux (e.g., RPi 5):** The `prev_pid` field is located at **offset 24**.
* **Tegra Linux 5.15 (Jetson):** The `prev_pid` field is shifted to **offset 28** due to custom padding.

If a standard eBPF program attempts to read a Tegra kernel, it will read the wrong bytes, interpreting garbage data as Process IDs. To solve this, this project compiles **two distinct eBPF binaries** with identical logic but different memory maps. The Go agent detects the host hardware at runtime and transparently loads the correct binary, ensuring accurate telemetry across the heterogeneous cluster.

### The Thermal Event Algorithm
The core logic handles two distinct tasks: continuously updating the temperature reading and extracting the dynamic zone name (handled only once to minimize overhead).

![Thermal Algorithm](assets/alg2_thermal_ebpf.png)

The execution flow processes the raw kernel context `ctx` as follows:

1.  **Extract Temperature:** The program reads the integer value `ctx->temp`. Note that the kernel stores this in **millidegrees Celsius** (e.g., `45000` represents 45°C).
2.  **Resolve Zone Name:** Unlike fixed integer fields, the thermal zone name is a variable-length string located dynamically in the event blob. The program reads the `__data_loc` field, where the lower 16 bits contain the relative offset to the string data.
3.  **Optimization (Read-Once):** String copying (`bpf_probe_read_str`) is computationally expensive relative to simple integer reads. To maintain low overhead, the program checks a flag map. If the zone name has already been resolved for this boot session, it skips the string copy and only updates the temperature.
4.  **Update Telemetry:** The extracted temperature is written to a shared BPF map for the userspace agent to consume.

### Handling Platform Divergence
Similar to the CPU collector, the thermal tracepoint structure differs between standard Linux kernels and the NVIDIA Tegra kernel.

* **Standard Layout:** The `temp` field is located at **offset 20**.
* **Tegra Layout:** The `temp` field is shifted to **offset 24**.
* **Dynamic Pointer Logic:** The `__data_loc` field requires bitwise masking (`raw_loc & 0xFFFF`) to determine the start of the string.

To ensure correct data extraction, the agent loads a platform-specific BPF binary that aligns with the target device's memory layout, preventing the reading of misaligned bytes.

## Userspace Telemetry Collectors

The userspace agent acts as the control plane for the eBPF observability layer. While the kernel programs execute reactively to capture raw data, the userspace collectors are responsible for lifecycle management, data extraction, and normalization.

This design deliberately separates **measurement** (kernel space) from **interpretation** (user space), allowing each layer to remain simple and independently evolvable.

### Collector Lifecycle
Both the CPU and Thermal collectors follow a strict initialization and runtime lifecycle to ensure stability:

1.  **Resource Preparation:** The agent first removes the kernel memory lock limit (`RLIMIT_MEMLOCK`). This is a mandatory step for eBPF operations, as maps and programs are pinned kernel objects that require sufficient locked memory to load successfully.
2.  **Platform Detection:** The collector inspects the host's kernel release string to determine if it is running on a standard Linux kernel or an NVIDIA Tegra kernel.
3.  **Binary Selection:** Based on the platform, it loads the correct, pre-compiled eBPF binary (e.g., `cpu_core.o` vs `cpu_tegra.o`) to match the host's tracepoint layout.
4.  **Attachment:** The verified programs are attached to their respective hooks (e.g., `sched_switch`), transitioning the kernel logic to an event-driven state.
5.  **Streaming:** The agent periodically polls the BPF maps, aggregates raw counters into meaningful rates, and streams the normalized metrics to the scheduler.

###  CPU Usage Collector

The CPU collector diverges from traditional tools that sample `/proc/stat`. Instead, it derives node-level utilization directly from scheduler activity, converting per-PID cumulative runtime (in nanoseconds) into a precise CPU utilization percentage.

#### Platform Detection & Attachment
At startup, the collector inspects the kernel release string to determine the correct eBPF object to load. This step is critical for handling the memory layout differences between standard and Tegra kernels:

* **Standard Kernels:** Loads object expecting `prev_pid` at **offset 24**.
* **Tegra Kernels:** Loads object expecting `prev_pid` at **offset 28**.

By making this decision before loading, the system avoids the performance penalty of runtime conditionals inside the kernel code. Once the correct binary is selected, it is verified by the kernel and attached to the `sched_switch` and `sched_process_exit` hooks.

![Agent Loading Flow](assets/cpu_agent_flowchart1.png)

#### CPU Polling Algorithm
The collector polls the kernel maps once per second. To calculate the current load, it performs a **delta-based calculation**: comparing the total execution time recorded now against the time recorded during the previous check.

![Polling Logic Flow](assets/cpu_agent_flowchart2.png)

The specific algorithm used to normalize this data across multi-core systems is detailed below:

![CPU Algorithm](assets/alg3_cpu_userspace.png)

**The Logic Explained:**
1.  **Iterate:** The agent loops through the BPF map containing the cumulative runtime for every PID.
2.  **Calculate Delta:** For each process, it calculates how much it ran since the last second (`current_ns - prev_ns`).
3.  **Accumulate:** All process deltas are summed to get the `total_delta_ns` for the node.
4.  **Normalize:** To produce a percentage between 0-100%, the total runtime is divided by the "scaled interval" (Wall Time × Number of Cores). This prevents multi-core systems from reporting misleading values (e.g., 400% on a 4-core machine).

#### Key Design Decisions
* **Delta-Based Accounting:** By calculating the change rather than using absolute totals, the system isolates exactly what happened during the last second, preventing cumulative measurement drift.
* **Core-Scaled Normalization:** Scaling the interval by the number of logical cores ensures the metric remains intuitive (0-100%) regardless of the underlying hardware (e.g., Quad-core RPi vs 6-core Jetson).
* **PID-Agnostic Aggregation:** While the kernel tracks data per-process (PID), the userspace collector aggregates this into a single node-level signal, which is exactly what the scheduler needs for placement decisions.

###  Memory Pressure Collector

While CPU and Thermal metrics rely on complex kernel tracing, the Memory Collector adopts a pragmatic, capacity-based approach. It monitors system-wide memory pressure by polling standard OS interfaces rather than hooking into low-level allocator paths.

####  Rationale: Why Polling over eBPF?
In Linux kernel development, memory pressure is often observed via "direct reclaim" tracepoints. However, these events are rare in typical edge workloads and usually only trigger when the system is already thrashing.

For a scheduler designed to **prevent** saturation, waiting for a reclaim event is too late. Therefore, this system uses `/proc/meminfo` to derive a stable "Saturation" signal. This ensures:
* **Predictability:** `MemAvailable` is a standard kernel estimate of how much RAM can be allocated *without* swapping.
* **Portability:** This interface is stable across all Linux distributions and architectures, unlike internal allocator symbols which change frequently.

####  Implementation & Runtime Flow
The collector runs a lightweight userspace loop that reads the kernel's memory accounting structures once per second. Unlike the event-driven CPU collector, this is a polling-based architecture designed for stability.

![Memory Agent Flow](assets/mem_agent_flowchart1.png)

#### The Saturation Algorithm
The system calculates "Saturation" as the inverse of Availability. By focusing on `MemAvailable` rather than just "Free" memory, the metric correctly accounts for reclaimable page caches, which are crucial for performance on RAM-constrained devices arm devices.

![Memory Algorithm](assets/alg4_mem_userspace.png)

**The Logic Explained:**
1.  **Read:** The collector reads `/proc/meminfo`.
2.  **Extract:** It parses `MemTotal` (physical RAM) and `MemAvailable` (RAM usable without swapping).
3.  **Calculate:**
    * `Used = Total - Available`
    * `Saturation % = (Used / Total) * 100`
4.  **Emit:** This percentage is streamed to the scheduler as a "Safety Signal."

####  Key Design Decision
* **Predictive Avoidance:** The goal is to detect when a node is *approaching* limits, not just when it fails. `MemAvailable` provides this early warning buffer.

### Thermal Collector

The thermal collector monitors the physical state of the device to prevent hardware throttling. Unlike generic tools that rely on high-level vendor APIs (which often vary between Raspberry Pi and Jetson), this collector extracts raw hardware temperature telemetry directly from kernel tracepoints.

#### Platform Detection & Attachment
Similar to the CPU collector, the thermal agent must handle kernel heterogeneity at startup. It selects between standard and Tegra layouts to ensuring the `thermal:thermal_temperature` tracepoint is interpreted correctly.

* **Standard Kernels:** Loads object expecting `temp` at **offset 20**.
* **Tegra Kernels:** Loads object expecting `temp` at **offset 24**.

Once the platform is identified, the eBPF program is verified and attached.

![Thermal Agent Loading Flow](assets/thermal_agent_flowchart1.png)

#### Thermal Polling Algorithm
The collector reads raw hardware temperatures directly from the shared BPF maps once per second. The kernel reports this data in **millidegrees** (1/1000th of a degree), which the userspace collector converts into a human-readable format and classifies based on safety thresholds.

![Thermal Polling Logic](assets/thermal_agent_flowchart2.png)

The algorithm for conversion and classification is detailed below:

![Thermal Userspace Algorithm](assets/alg5_thermal_userspace.png)

**The Logic Explained:**
1.  **Wait for Discovery:** The loop first checks if the kernel probe has discovered any thermal zones (`zone_count > 0`). This prevents reporting invalid data during the boot sequence.
2.  **Retrieve:** It reads the raw temperature (`raw_temp_mC`) and the zone name (e.g., "CPU-therm") from the BPF maps.
3.  **Convert:** The value is divided by 1000 to convert millidegrees to standard Celsius (e.g., `65000` becomes `65°C`).
4.  **Classify:** The system applies an hardcoded policy for now to determine the safety state:
    * **> 80°C:** `HOT` (Throttling imminent)
    * **> 60°C:** `WARM` (Active load)
    * **< 60°C:** `SAFE` (Cool)

#### Key Design Decisions
* **Policy vs. Mechanism:** The kernel simply reports the raw number. The decision of whether that number is "HOT" or "SAFE" happens entirely in userspace. This allows safety thresholds to be tuned without recompiling the kernel programs.
* **Millidegree Precision:** The system maintains the kernel's native precision throughout the pipeline, ensuring no data is lost due to premature rounding.
* **Dynamic Zone Discovery:** By waiting for the `zone_count` flag, the collector handles the asynchronous nature of hardware sensor initialization robustly, avoiding "zero" or "null" readings at startup.

