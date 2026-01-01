package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Verbose controls whether debug logs are printed.
var Verbose bool

var rootCmd = &cobra.Command{
	Use:   "ebpf_edge",
	Short: "Decentralized-Kernel-Metric-Driven-Scheduling-for-Edge-Faas",
	Long:  "Kernel-metric driven orchestration for decentralized edge computing",
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Global flags can be added here
	rootCmd.PersistentFlags().BoolVarP(&Verbose, "verbose", "v", false, "Enable verbose output")
}

// logDebug prints only if the --verbose flag is set.
func logDebug(format string, a ...any) {
	if Verbose {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}
