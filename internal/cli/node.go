package cli

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
	"stresstool/internal/protocol"
	"stresstool/internal/runner"
	"stresstool/internal/security"
)

// Node represents a worker node that executes tests
type Node struct {
	nodeName       string
	controllerAddr string
	verbose        bool
	tlsConfig      *security.TLSConfig
	cmdPolicy      *security.CommandPolicy

	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder

	config   *config.Config
	parallel bool
}

// RunNode starts a node and connects to the controller
func RunNode(nodeName, controllerAddr string, verbose bool, tlsCfg *security.TLSConfig, cmdPolicy *security.CommandPolicy) error {
	if nodeName == "" {
		return fmt.Errorf("node name is required")
	}
	if controllerAddr == "" {
		return fmt.Errorf("controller address is required")
	}

	// Default to secure policy if not specified
	if cmdPolicy == nil {
		cmdPolicy = security.DefaultSecurePolicy()
		fmt.Println("⚠️  Using default security policy: remote command execution is DISABLED")
	}

	// Warn if TLS is not enabled
	if tlsCfg == nil || !tlsCfg.Enabled {
		fmt.Println("⚠️  WARNING: TLS is not enabled. Communication is unencrypted and unauthenticated.")
		fmt.Println("   This mode should only be used on trusted networks.")
		fmt.Println("   Received configs may contain arbitrary commands or sensitive data.\n")
	} else {
		fmt.Println("✓ TLS enabled for secure communication")
	}

	node := &Node{
		nodeName:       nodeName,
		controllerAddr: controllerAddr,
		verbose:        verbose,
		tlsConfig:      tlsCfg,
		cmdPolicy:      cmdPolicy,
	}

	return node.start()
}

func (n *Node) start() error {
	// Connect to controller
	var conn net.Conn
	var err error

	if n.tlsConfig != nil && n.tlsConfig.Enabled {
		tlsConf, err := security.LoadClientTLSConfig(n.tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to load TLS config: %w", err)
		}

		conn, err = tls.Dial("tcp", n.controllerAddr, tlsConf)
		if err != nil {
			return fmt.Errorf("failed to connect to controller at %s with TLS: %w", n.controllerAddr, err)
		}
	} else {
		conn, err = net.Dial("tcp", n.controllerAddr)
		if err != nil {
			return fmt.Errorf("failed to connect to controller at %s: %w", n.controllerAddr, err)
		}
	}
	defer conn.Close()

	n.conn = conn
	n.encoder = json.NewEncoder(conn)
	n.decoder = json.NewDecoder(bufio.NewReader(conn))

	// Send hello message
	helloMsg := protocol.Message{
		Type: protocol.MsgTypeHello,
		Data: protocol.HelloMessage{
			NodeName: n.nodeName,
			Version:  "1.0.0",
		},
	}

	if err := n.encoder.Encode(helloMsg); err != nil {
		return fmt.Errorf("failed to send hello message: %w", err)
	}

	fmt.Printf("Connected to controller at %s as node '%s'\n", n.controllerAddr, n.nodeName)
	fmt.Println("Waiting for test specifications from controller...")

	// Listen for messages from controller
	return n.handleMessages()
}

func (n *Node) handleMessages() error {
	for n.decoder.More() {
		var msg protocol.Message
		if err := n.decoder.Decode(&msg); err != nil {
			return fmt.Errorf("failed to decode message: %w", err)
		}

		switch msg.Type {
		case protocol.MsgTypeTestSpec:
			specData, err := parseMessageData[protocol.TestSpecMessage](msg.Data)
			if err != nil {
				return fmt.Errorf("failed to parse test spec: %w", err)
			}
			n.handleTestSpec(specData)

		case protocol.MsgTypeStartTests:
			if err := n.executeTests(); err != nil {
				return fmt.Errorf("failed to execute tests: %w", err)
			}

		case protocol.MsgTypeComplete:
			fmt.Println("All tests complete. Disconnecting...")
			return nil
		}
	}

	return nil
}

func (n *Node) handleTestSpec(spec *protocol.TestSpecMessage) {
	// SECURITY NOTE: Command validation happens after receiving the config.
	// This means if the config contains sensitive data along with unauthorized commands,
	// that data has already been transmitted. For maximum security:
	// 1. Use TLS to encrypt the channel
	// 2. Only connect to trusted controllers
	// 3. Use the default secure policy to block all remote commands
	
	// Validate commands in config against policy
	for _, funcDef := range spec.Config.Funcs {
		if err := n.cmdPolicy.ValidateCommand(funcDef.Cmd); err != nil {
			fmt.Printf("⚠️  SECURITY: Rejecting config due to unauthorized command: %v\n", err)
			fmt.Printf("   Function: %s, Command: %v\n", funcDef.Name, funcDef.Cmd)
			fmt.Println("   The controller attempted to send a config with commands that are not allowed.")
			fmt.Println("   This could be a security issue. Check controller configuration.")
			return
		}
	}

	n.config = spec.Config.WithNodeOverrides(spec.NodeName)
	n.parallel = spec.Parallel

	if err := n.config.Validate(); err != nil {
		fmt.Printf("Config validation failed: %v\n", err)
		return
	}

	fmt.Printf("Received test specification with %d test(s)\n", len(n.config.Tests))
	if len(spec.Config.Funcs) > 0 {
		fmt.Printf("✓ Validated %d custom function(s) against security policy\n", len(spec.Config.Funcs))
	}
	if n.verbose {
		for _, test := range n.config.Tests {
			fmt.Printf("  - %s: %d RPS, %d threads, %ds duration\n",
				test.Name, test.RequestsPerSecond, test.Threads, test.RunSeconds)
		}
	}

	// Send ready message
	readyMsg := protocol.Message{
		Type: protocol.MsgTypeReady,
		Data: protocol.ReadyMessage{
			NodeName: n.nodeName,
		},
	}

	if err := n.encoder.Encode(readyMsg); err != nil {
		fmt.Printf("Failed to send ready message: %v\n", err)
	}

	fmt.Println("Ready to start tests. Waiting for start signal...")
}

func (n *Node) executeTests() error {
	if n.config == nil {
		return fmt.Errorf("no test configuration received")
	}

	fmt.Printf("\nStarting test execution...\n\n")

	// Create evaluator
	eval := placeholders.NewEvaluator(n.config)

	// Create runner
	r := runner.NewRunner(eval, n.verbose)

	// Progress channel
	progressChan := make(chan runner.ProgressUpdate, 100)

	// Start progress reporter
	go n.reportProgress(progressChan)

	// Run tests
	results := make([]*runner.TestResult, len(n.config.Tests))

	if n.parallel {
		var wg sync.WaitGroup
		for i := range n.config.Tests {
			wg.Add(1)
			test := n.config.Tests[i]
			index := i
			go func(t config.Test, resultIndex int) {
				defer wg.Done()
				results[resultIndex] = r.RunTest(&t, progressChan)
			}(test, index)
		}
		wg.Wait()
	} else {
		for i := range n.config.Tests {
			test := n.config.Tests[i]
			results[i] = r.RunTest(&test, progressChan)
		}
	}

	close(progressChan)
	time.Sleep(100 * time.Millisecond) // Allow final progress update

	// Send results to controller
	for _, result := range results {
		resultMsg := protocol.Message{
			Type: protocol.MsgTypeTestResult,
			Data: protocol.TestResultMessage{
				NodeName: n.nodeName,
				TestName: result.Test.Name,
				Result:   result,
			},
		}

		if err := n.encoder.Encode(resultMsg); err != nil {
			fmt.Printf("Failed to send test result: %v\n", err)
		}
	}

	fmt.Println("\n✓ All tests completed. Results sent to controller.")

	return nil
}

func (n *Node) reportProgress(progressChan <-chan runner.ProgressUpdate) {
	for update := range progressChan {
		// Send to controller
		progressMsg := protocol.Message{
			Type: protocol.MsgTypeProgress,
			Data: protocol.ProgressMessage{
				NodeName: n.nodeName,
				TestName: update.TestName,
				Elapsed:  update.Elapsed.Seconds(),
				Total:    update.Total,
				Failures: update.Failures,
				RPS:      update.RPS,
				Done:     update.Done,
			},
		}

		if err := n.encoder.Encode(progressMsg); err != nil {
			if n.verbose {
				fmt.Printf("Failed to send progress update: %v\n", err)
			}
		}

		// Also display locally (optional)
		if n.verbose {
			if update.Done {
				fmt.Printf("✓ %s: COMPLETE - %d requests, %.1f RPS, %d failures\n",
					update.TestName, update.Total, update.RPS, update.Failures)
			} else {
				fmt.Printf("→ %s: %.0fs elapsed - %d requests, %.1f RPS, %d failures\n",
					update.TestName, update.Elapsed.Seconds(), update.Total, update.RPS, update.Failures)
			}
		}
	}
}
