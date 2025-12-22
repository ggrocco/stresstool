package main

import (
	"fmt"
	"os"

	"stresstool/internal/cli"

	"github.com/spf13/cobra"
)

var (
	configFile string
	verbose    bool
	dryRun     bool
	parallel   bool
)

var rootCmd = &cobra.Command{
	Use:   "stresstool",
	Short: "HTTP stress test tool with YAML configuration",
	Long:  "A command-line HTTP stress test tool that reads YAML configuration files and executes concurrent HTTP requests with assertions.",
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run stress tests from a YAML configuration file",
	Long:  "Execute HTTP stress tests defined in a YAML configuration file.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if configFile == "" {
			return fmt.Errorf("config file is required (use -f or --file)")
		}

		if err := cli.Run(configFile, verbose, dryRun, parallel); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		return nil
	},
}

func init() {
	runCmd.Flags().StringVarP(&configFile, "file", "f", "", "Path to YAML configuration file (required)")
	runCmd.MarkFlagRequired("file")
	runCmd.Flags().BoolVar(&verbose, "verbose", false, "Print detailed logs")
	runCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate config and show planned tests without executing HTTP calls")
	runCmd.Flags().BoolVar(&parallel, "parallel", false, "Run all specs in parallel")

	rootCmd.AddCommand(runCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
