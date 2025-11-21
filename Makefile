.PHONY: all update upgrade install_llvm install_clang install_bpftool install_go install_bpftrace set_path source_profile build generate clean

install: update upgrade install_llvm install_clang install_go install_bpftrace install_bpftool set_path source_profile

update:
	sudo apt update -y || { echo "Update failed"; exit 1; }

upgrade:
	sudo apt upgrade -y || { echo "Upgrade failed"; exit 1; }

install_llvm:
	sudo apt install -y llvm || { echo "LLVM installation failed"; exit 1; }

install_clang:
	sudo apt install -y clang || { echo "Clang installation failed"; exit 1; }

install_bpftrace:
	sudo apt install -y bpftrace || { echo "bpftrace installation failed"; exit 1; }

install_go:
	wget https://golang.org/dl/go1.20.2.linux-amd64.tar.gz || { echo "Go download failed"; exit 1; }
	sudo tar -C /usr/local -xzf go1.20.2.linux-amd64.tar.gz || { echo "Go extraction failed"; exit 1; }

install_bpftool:
	sudo apt install -y bpftool || { echo "bpftool installation failed, falling back to linux-tools-common"; sudo apt install -y linux-tools-common; }

set_path:
	echo "export PATH=/usr/local/go/bin/go:${PATH}" | sudo tee -a $HOME/.profile || { echo "Setting PATH failed"; exit 1; }

source_profile:
	source $HOME/.profile || { echo "Sourcing profile failed"; exit 1; }

gen_vmlinux:
	sudo bpftool btf dump file /sys/kernel/btf/vmlinux format c > ./bpf/headers/vmlinux.h

# Generate eBPF Go bindings
generate:
	go generate ./cmd/procwatch.go

# Build the ebpf_edge CLI application
build: generate
	go build -o ebpf_edge .

# Clean generated files
clean:
	rm -f cmd/*_bpfel_x86.go
	rm -f cmd/*_bpfel_x86.o
	rm -f ebpf_edge

# Install dependencies
deps:
	go mod tidy
	go mod download
