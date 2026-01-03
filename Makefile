SHELL := /bin/bash

.PHONY: install update upgrade install_llvm install_clang install_go \
        install_bpftrace  deps generate build clean doctor

#######################################################################
# MAIN INSTALL TARGET
#######################################################################

install: update upgrade install_llvm install_clang install_go \
         install_bpftrace

#######################################################################
# SYSTEM UPDATE
#######################################################################

update:
	sudo apt update -y

upgrade:
	sudo apt upgrade -y

#######################################################################
# INSTALL LLVM + CLANG (ONLY IF MISSING)
#######################################################################

install_llvm:
	@if ! command -v llvm-strip >/dev/null 2>&1; then \
		echo "Installing LLVM..."; \
		sudo apt install -y llvm; \
	else \
		echo "✓ LLVM already installed"; \
	fi

install_clang:
	@if ! command -v clang >/dev/null 2>&1; then \
		echo "Installing Clang..."; \
		sudo apt install -y clang; \
	else \
		echo "✓ Clang already installed"; \
	fi

#######################################################################
# INSTALL GO (ONLY IF MISSING)
#######################################################################

install_go:
	@if ! command -v go >/dev/null 2>&1; then \
		echo "Installing Go 1.20.2..."; \
		wget https://golang.org/dl/go1.20.2.linux-amd64.tar.gz; \
		sudo rm -rf /usr/local/go; \
		sudo tar -C /usr/local -xzf go1.20.2.linux-amd64.tar.gz; \
	else \
		echo "✓ Go already installed"; \
	fi

#######################################################################
# INSTALL bpftrace (ONLY IF MISSING)
#######################################################################

install_bpftrace:
	@if ! command -v bpftrace >/dev/null 2>&1; then \
		echo "Installing bpftrace..."; \
		sudo apt install -y bpftrace; \
	else \
		echo "✓ bpftrace already installed"; \
	fi

#######################################################################
# GO MODULE DEPENDENCIES
#######################################################################

deps:
	go mod tidy
	go mod download

#######################################################################
# GENERATE BPF GO BINDINGS
#######################################################################

generate:
	go generate ./cmd/tempwatch.go
	go generate ./cmd/cpuwatch.go

# Generate gRPC Proto files
proto_generate:
	protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    proto/metrics.proto

#######################################################################
# BUILD GO BINARY
#######################################################################

build-amd64:
	@echo "Building for AMD64 (Host)..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ebpf_edge_amd64_d .

build-arm64:
	@echo "Building for ARM64 (Jetson/Pi)..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ebpf_edge_arm64_d .

build-prod:
	@echo "Building Production Binaries (Stripped)..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o ebpf_edge_amd64_p .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-s -w" -o ebpf_edge_arm64_p .

#######################################################################
# CLEAN GENERATED FILES
#######################################################################

clean:
	rm -f cmd/*_bpfel_x86.go cmd/*_bpfel_x86.o ebpf_edge

#######################################################################
# ENVIRONMENT DOCTOR
#######################################################################

doctor:
	@echo "Checking Go..."; go version
	@echo "Checking Clang..."; clang --version | head -1
	@echo "Checking bpftool..."
	-@bpftool version || echo "bpftool mismatch warning expected on Ubuntu 6.14 HWE kernels."
	@echo "Checking bpftrace..."; bpftrace --version
