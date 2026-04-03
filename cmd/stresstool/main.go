package main

import (
	"fmt"
	"os"

	"stresstool/internal/cli"
	"stresstool/internal/version"

	"github.com/spf13/cobra"
)

var (
	configFile     string
	verbose        bool
	dryRun         bool
	parallel       bool
	nodeName       string
	controllerAddr string
	listenAddr     string
	webUI          bool
	webPort        int
	tlsCert        string
	tlsKey         string
	tlsCA          string
	tlsInsecure    bool
)

var rootCmd = &cobra.Command{
	Use:     "stresstool",
	Short:   "HTTP stress test tool with YAML configuration",
	Long:    "A command-line HTTP stress test tool that reads YAML configuration files and executes concurrent HTTP requests with assertions.",
	Version: version.String(),
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("stresstool " + version.String())
	},
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run stress tests from a YAML configuration file (standalone mode)",
	Long:  "Execute HTTP stress tests defined in a YAML configuration file in standalone mode (no distributed execution).",
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

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Run this instance as a worker node",
	Long:  "Connect to a controller and wait for test specifications to execute.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if nodeName == "" {
			return fmt.Errorf("node name is required (use --node-name)")
		}
		if controllerAddr == "" {
			return fmt.Errorf("controller address is required (use --controller)")
		}

		if err := cli.RunNode(nodeName, controllerAddr, verbose, tlsOptsFromFlags()); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		return nil
	},
}

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Start controller and coordinate test execution across nodes",
	Long:  "Start a controller server that loads test configuration, waits for nodes to connect, and coordinates distributed test execution.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if configFile == "" && !webUI {
			return fmt.Errorf("config file is required (use -f or --file), or use --web to configure via the UI")
		}

		uiAddr := ""
		if webUI {
			uiAddr = fmt.Sprintf(":%d", webPort)
		}
		if err := cli.RunController(listenAddr, configFile, uiAddr, parallel, verbose, tlsOptsFromFlags()); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		return nil
	},
}

func init() {
	// Run command (standalone mode)
	runCmd.Flags().StringVarP(&configFile, "file", "f", "", "Path to YAML configuration file (required)")
	runCmd.MarkFlagRequired("file")
	runCmd.Flags().BoolVar(&verbose, "verbose", false, "Print detailed logs")
	runCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate config and show planned tests without executing HTTP calls")
	runCmd.Flags().BoolVar(&parallel, "parallel", false, "Run all specs in parallel")
	rootCmd.AddCommand(runCmd)

	// Node command (worker)
	nodeCmd.Flags().StringVar(&nodeName, "node-name", "", "Name of this node (required)")
	nodeCmd.MarkFlagRequired("node-name")
	nodeCmd.Flags().StringVar(&controllerAddr, "controller", "", "Controller address (host:port) to connect to (required)")
	nodeCmd.MarkFlagRequired("controller")
	nodeCmd.Flags().BoolVar(&verbose, "verbose", false, "Print detailed logs")
	rootCmd.AddCommand(nodeCmd)

	// Controller command (coordinator)
	controllerCmd.Flags().StringVarP(&configFile, "file", "f", "", "Path to YAML configuration file (optional with --web)")
	controllerCmd.Flags().StringVar(&listenAddr, "listen", ":8090", "Address for controller to listen on")
	controllerCmd.Flags().BoolVar(&webUI, "web", false, "Enable the web UI for triggering tests")
	controllerCmd.Flags().IntVar(&webPort, "web-port", 8091, "Port for the web UI server (requires --web)")
	controllerCmd.Flags().BoolVar(&parallel, "parallel", false, "Run tests in parallel on each node")
	controllerCmd.Flags().BoolVar(&verbose, "verbose", false, "Print detailed logs")
	rootCmd.AddCommand(controllerCmd)

	rootCmd.AddCommand(versionCmd)

	// gRPC TLS: use --insecure for plaintext (default). Omit --insecure and pass cert flags to enable TLS.
	nodeCmd.Flags().BoolVar(&tlsInsecure, "insecure", true, "Use plaintext gRPC (set false when using TLS)")
	nodeCmd.Flags().StringVar(&tlsCert, "tls-cert", "", "Path to TLS client certificate (mTLS)")
	nodeCmd.Flags().StringVar(&tlsKey, "tls-key", "", "Path to TLS client private key (mTLS)")
	nodeCmd.Flags().StringVar(&tlsCA, "tls-ca", "", "Path to CA certificate to verify the controller")

	controllerCmd.Flags().BoolVar(&tlsInsecure, "insecure", true, "Use plaintext gRPC (set false when using TLS)")
	controllerCmd.Flags().StringVar(&tlsCert, "tls-cert", "", "Path to TLS server certificate")
	controllerCmd.Flags().StringVar(&tlsKey, "tls-key", "", "Path to TLS server private key")
	controllerCmd.Flags().StringVar(&tlsCA, "tls-ca", "", "Path to CA for verifying client certificates (mTLS)")
}

func tlsOptsFromFlags() cli.TLSOptions {
	return cli.TLSOptions{
		CertFile: tlsCert,
		KeyFile:  tlsKey,
		CAFile:   tlsCA,
		Insecure: tlsInsecure,
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
