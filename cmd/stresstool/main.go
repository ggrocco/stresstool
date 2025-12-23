package main

import (
	"fmt"
	"os"

	"stresstool/internal/cli"
	"stresstool/internal/security"

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

	// TLS options
	tlsEnabled       bool
	tlsCert          string
	tlsKey           string
	tlsCA            string
	tlsClientCert    string
	tlsClientKey     string

	// Security options
	allowRemoteCommands       bool
	allowedCommands           []string
	unsafeAllowAllCommands    bool
	tlsServerName             string
)

var rootCmd = &cobra.Command{
	Use:   "stresstool",
	Short: "HTTP stress test tool with YAML configuration",
	Long:  "A command-line HTTP stress test tool that reads YAML configuration files and executes concurrent HTTP requests with assertions.",
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

		// Setup TLS config
		var tlsCfg *security.TLSConfig
		if tlsEnabled {
			tlsCfg = &security.TLSConfig{
				Enabled:        true,
				CAFile:         tlsCA,
				ClientCertFile: tlsClientCert,
				ClientKeyFile:  tlsClientKey,
				ServerName:     tlsServerName,
			}
		}

		// Setup command policy
		var cmdPolicy *security.CommandPolicy
		if allowRemoteCommands {
			if len(allowedCommands) > 0 {
				cmdPolicy = security.AllowlistPolicy(allowedCommands...)
			} else if unsafeAllowAllCommands {
				// Allow all commands (backward compatible but unsafe)
				// Requires explicit flag to enable
				fmt.Println("⚠️  WARNING: Allowing ALL remote commands without restrictions!")
				fmt.Println("   This is UNSAFE and should only be used on fully trusted networks.")
				cmdPolicy = &security.CommandPolicy{
					AllowRemoteCommands: true,
					AllowedCommands:     []string{},
				}
			} else {
				return fmt.Errorf("--allow-remote-commands requires either --allowed-commands or --unsafe-allow-all-commands")
			}
		} else {
			cmdPolicy = security.DefaultSecurePolicy()
		}

		if err := cli.RunNode(nodeName, controllerAddr, verbose, tlsCfg, cmdPolicy); err != nil {
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
		if configFile == "" {
			return fmt.Errorf("config file is required (use -f or --file)")
		}

		// Setup TLS config
		var tlsCfg *security.TLSConfig
		if tlsEnabled {
			tlsCfg = &security.TLSConfig{
				Enabled:  true,
				CertFile: tlsCert,
				KeyFile:  tlsKey,
				CAFile:   tlsCA,
			}
		}

		if err := cli.RunController(listenAddr, configFile, parallel, verbose, tlsCfg); err != nil {
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
	
	// TLS options for node
	nodeCmd.Flags().BoolVar(&tlsEnabled, "tls", false, "Enable TLS for secure communication")
	nodeCmd.Flags().StringVar(&tlsCA, "tls-ca", "", "Path to CA certificate file for verifying controller")
	nodeCmd.Flags().StringVar(&tlsClientCert, "tls-cert", "", "Path to client certificate file for mutual TLS")
	nodeCmd.Flags().StringVar(&tlsClientKey, "tls-key", "", "Path to client key file for mutual TLS")
	nodeCmd.Flags().StringVar(&tlsServerName, "tls-server-name", "", "Expected server name for certificate verification")
	
	// Security options for node
	nodeCmd.Flags().BoolVar(&allowRemoteCommands, "allow-remote-commands", false, "Allow execution of commands from controller config (requires --allowed-commands or --unsafe-allow-all-commands)")
	nodeCmd.Flags().StringSliceVar(&allowedCommands, "allowed-commands", []string{}, "Comma-separated list of allowed command prefixes (e.g., 'echo,/usr/bin/curl')")
	nodeCmd.Flags().BoolVar(&unsafeAllowAllCommands, "unsafe-allow-all-commands", false, "UNSAFE: Allow all commands without restrictions (only for trusted networks)")
	
	rootCmd.AddCommand(nodeCmd)

	// Controller command (coordinator)
	controllerCmd.Flags().StringVarP(&configFile, "file", "f", "", "Path to YAML configuration file (required)")
	controllerCmd.MarkFlagRequired("file")
	controllerCmd.Flags().StringVar(&listenAddr, "listen", ":8090", "Address for controller to listen on")
	controllerCmd.Flags().BoolVar(&parallel, "parallel", false, "Run tests in parallel on each node")
	controllerCmd.Flags().BoolVar(&verbose, "verbose", false, "Print detailed logs")
	
	// TLS options for controller
	controllerCmd.Flags().BoolVar(&tlsEnabled, "tls", false, "Enable TLS for secure communication")
	controllerCmd.Flags().StringVar(&tlsCert, "tls-cert", "", "Path to server certificate file")
	controllerCmd.Flags().StringVar(&tlsKey, "tls-key", "", "Path to server key file")
	controllerCmd.Flags().StringVar(&tlsCA, "tls-ca", "", "Path to CA certificate file for mutual TLS (client verification)")
	
	rootCmd.AddCommand(controllerCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
