package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/protocol"
	"stresstool/internal/runner"
)

// Controller manages test execution across multiple nodes
type Controller struct {
	listenAddr string
	config     *config.Config
	parallel   bool
	verbose    bool

	nodesMutex sync.Mutex
	nodes      map[string]*NodeConnection

	progressMutex sync.Mutex
	progress      map[string]map[string]*protocol.ProgressMessage
	lineCount     int

	resultsMutex sync.Mutex
	results      map[string]map[string]*runner.TestResult
}

// NodeConnection represents a connected node
type NodeConnection struct {
	Name    string
	Conn    net.Conn
	Encoder *json.Encoder
	Decoder *json.Decoder
}

// RunController starts the controller and waits for nodes to connect
func RunController(listenAddr, configFile string, parallel, verbose bool) error {
	// Load configuration
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	ctrl := &Controller{
		listenAddr: listenAddr,
		config:     cfg,
		parallel:   parallel,
		verbose:    verbose,
		nodes:      make(map[string]*NodeConnection),
		progress:   make(map[string]map[string]*protocol.ProgressMessage),
		results:    make(map[string]map[string]*runner.TestResult),
	}

	return ctrl.start()
}

func (c *Controller) start() error {
	ln, err := net.Listen("tcp", c.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", c.listenAddr, err)
	}
	defer ln.Close()

	fmt.Printf("Controller listening on %s\n", c.listenAddr)
	fmt.Printf("Loaded config with %d test(s)\n", len(c.config.Tests))
	fmt.Println("\nWaiting for nodes to connect...")
	fmt.Println("Press Ctrl+C to cancel, or wait for nodes and then type 'start' to begin tests")

	// Start accepting connections in background
	connChan := make(chan net.Conn)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connChan <- conn
		}
	}()

	// Handle user input and connections
	inputChan := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputChan <- scanner.Text()
		}
	}()

	// Simple input handler - wait for "start" command
	fmt.Println("\nType 'start' when ready to begin tests, or 'nodes' to see connected nodes:")
	
	for {
		select {
		case conn := <-connChan:
			go c.handleNodeConnection(conn)
		case input := <-inputChan:
			switch input {
			case "start":
				return c.startTests()
			case "nodes":
				c.printConnectedNodes()
			default:
				fmt.Println("Unknown command. Type 'start' to begin or 'nodes' to list connected nodes.")
			}
		}
	}
}

func (c *Controller) handleNodeConnection(conn net.Conn) {
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	// Read hello message
	var msg protocol.Message
	if err := decoder.Decode(&msg); err != nil {
		fmt.Printf("Failed to decode hello message: %v\n", err)
		conn.Close()
		return
	}

	if msg.Type != protocol.MsgTypeHello {
		fmt.Printf("Expected hello message, got: %s\n", msg.Type)
		conn.Close()
		return
	}

	// Parse hello data
	helloData, err := parseMessageData[protocol.HelloMessage](msg.Data)
	if err != nil {
		fmt.Printf("Failed to parse hello message: %v\n", err)
		conn.Close()
		return
	}

	nodeName := helloData.NodeName
	fmt.Printf("✓ Node connected: %s\n", nodeName)

	// Store node connection
	c.nodesMutex.Lock()
	c.nodes[nodeName] = &NodeConnection{
		Name:    nodeName,
		Conn:    conn,
		Encoder: encoder,
		Decoder: decoder,
	}
	c.nodesMutex.Unlock()

	// Listen for messages from this node
	c.handleNodeMessages(nodeName, decoder)

	// Clean up on disconnect
	c.nodesMutex.Lock()
	delete(c.nodes, nodeName)
	c.nodesMutex.Unlock()
	fmt.Printf("✗ Node disconnected: %s\n", nodeName)
}

func (c *Controller) handleNodeMessages(nodeName string, decoder *json.Decoder) {
	for decoder.More() {
		var msg protocol.Message
		if err := decoder.Decode(&msg); err != nil {
			if c.verbose {
				fmt.Printf("Error decoding message from %s: %v\n", nodeName, err)
			}
			return
		}

		switch msg.Type {
		case protocol.MsgTypeProgress:
			progressData, err := parseMessageData[protocol.ProgressMessage](msg.Data)
			if err != nil {
				fmt.Printf("Failed to parse progress message: %v\n", err)
				continue
			}
			c.handleProgress(progressData)

		case protocol.MsgTypeTestResult:
			resultData, err := parseMessageData[protocol.TestResultMessage](msg.Data)
			if err != nil {
				fmt.Printf("Failed to parse test result message: %v\n", err)
				continue
			}
			c.handleTestResult(resultData)

		case protocol.MsgTypeReady:
			if c.verbose {
				fmt.Printf("Node %s is ready\n", nodeName)
			}
		}
	}
}

func (c *Controller) handleProgress(progress *protocol.ProgressMessage) {
	c.progressMutex.Lock()
	defer c.progressMutex.Unlock()

	if _, ok := c.progress[progress.NodeName]; !ok {
		c.progress[progress.NodeName] = make(map[string]*protocol.ProgressMessage)
	}
	c.progress[progress.NodeName][progress.TestName] = progress

	// Redraw progress
	if c.lineCount > 0 {
		fmt.Printf("\033[%dA", c.lineCount)
	}
	c.lineCount = 0

	for nodeName, nodeProgress := range c.progress {
		for _, p := range nodeProgress {
			prefix := fmt.Sprintf("%s / %s", nodeName, p.TestName)
			if p.Done {
				fmt.Printf("✓ %s: COMPLETE - %d requests, %.1f RPS, %d failures\n",
					prefix, p.Total, p.RPS, p.Failures)
			} else {
				fmt.Printf("→ %s: %.0fs elapsed - %d requests, %.1f RPS, %d failures\n",
					prefix, p.Elapsed, p.Total, p.RPS, p.Failures)
			}
			c.lineCount++
		}
	}
}

func (c *Controller) handleTestResult(result *protocol.TestResultMessage) {
	c.resultsMutex.Lock()
	defer c.resultsMutex.Unlock()

	if _, ok := c.results[result.NodeName]; !ok {
		c.results[result.NodeName] = make(map[string]*runner.TestResult)
	}
	c.results[result.NodeName][result.TestName] = result.Result

	if c.verbose {
		fmt.Printf("Received result from %s for test %s\n", result.NodeName, result.TestName)
	}
}

func (c *Controller) printConnectedNodes() {
	c.nodesMutex.Lock()
	defer c.nodesMutex.Unlock()

	fmt.Printf("\nConnected nodes (%d):\n", len(c.nodes))
	for nodeName := range c.nodes {
		fmt.Printf("  - %s\n", nodeName)
	}
	fmt.Println()
}

func (c *Controller) startTests() error {
	c.nodesMutex.Lock()
	if len(c.nodes) == 0 {
		c.nodesMutex.Unlock()
		return fmt.Errorf("no nodes connected")
	}

	fmt.Printf("\nStarting tests on %d node(s)...\n\n", len(c.nodes))

	// Send test spec to each node
	for nodeName, node := range c.nodes {
		spec := protocol.TestSpecMessage{
			Config:   c.config,
			NodeName: nodeName,
			Parallel: c.parallel,
		}

		msg := protocol.Message{
			Type: protocol.MsgTypeTestSpec,
			Data: spec,
		}

		if err := node.Encoder.Encode(msg); err != nil {
			fmt.Printf("Failed to send test spec to %s: %v\n", nodeName, err)
		}
	}
	c.nodesMutex.Unlock()

	// Wait a bit for nodes to prepare
	time.Sleep(500 * time.Millisecond)

	// Send start signal
	c.nodesMutex.Lock()
	startMsg := protocol.Message{
		Type: protocol.MsgTypeStartTests,
		Data: protocol.StartTestsMessage{
			Timestamp: time.Now().Unix(),
		},
	}

	for nodeName, node := range c.nodes {
		if err := node.Encoder.Encode(startMsg); err != nil {
			fmt.Printf("Failed to send start signal to %s: %v\n", nodeName, err)
		}
	}
	c.nodesMutex.Unlock()

	// Wait for all tests to complete
	c.waitForCompletion()

	// Print final summary
	c.printFinalSummary()

	// Notify all nodes that the test run is complete so they can disconnect
	c.nodesMutex.Lock()
	completeMsg := protocol.Message{
		Type: protocol.MsgTypeComplete,
		Data: nil,
	}
	for nodeName, node := range c.nodes {
		if err := node.Encoder.Encode(completeMsg); err != nil {
			fmt.Printf("Failed to send complete signal to %s: %v\n", nodeName, err)
		}
	}
	c.nodesMutex.Unlock()
	return nil
}

func (c *Controller) waitForCompletion() {
	expectedResults := make(map[string]map[string]bool)
	c.nodesMutex.Lock()
	for nodeName := range c.nodes {
		expectedResults[nodeName] = make(map[string]bool)
		for _, test := range c.config.Tests {
			expectedResults[nodeName][test.Name] = false
		}
	}
	c.nodesMutex.Unlock()

	// Poll for completion
	for {
		time.Sleep(1 * time.Second)

		c.resultsMutex.Lock()
		allComplete := true
	checkResults:
		for nodeName, tests := range expectedResults {
			for testName := range tests {
				if _, ok := c.results[nodeName]; !ok {
					allComplete = false
					break checkResults
				}
				if _, ok := c.results[nodeName][testName]; !ok {
					allComplete = false
					break checkResults
				}
			}
		}
		c.resultsMutex.Unlock()

		if allComplete {
			break
		}
	}

	// Move cursor down after progress display
	c.progressMutex.Lock()
	if c.lineCount > 0 {
		fmt.Printf("\033[%dB", c.lineCount)
	}
	c.progressMutex.Unlock()
}

func (c *Controller) printFinalSummary() {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("DISTRIBUTED TEST SUMMARY")
	fmt.Println(strings.Repeat("=", 80))

	c.resultsMutex.Lock()
	defer c.resultsMutex.Unlock()

	allPassed := true
	for nodeName, tests := range c.results {
		fmt.Printf("\n=== Node: %s ===\n", nodeName)
		for testName, result := range tests {
			fmt.Printf("\nTest: %s\n", testName)
			printTestResult(result)
			if !result.Passed {
				allPassed = false
			}
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	if allPassed {
		fmt.Println("OVERALL RESULT: ✓ ALL TESTS PASSED")
	} else {
		fmt.Println("OVERALL RESULT: ✗ SOME TESTS FAILED")
	}
	fmt.Println(strings.Repeat("=", 80))
}

func printTestResult(result *runner.TestResult) {
	test := result.Test
	metrics := result.Metrics

	fmt.Printf("  Path: %s %s\n", test.Method, test.Path)
	fmt.Printf("  Duration: %ds\n", test.RunSeconds)
	fmt.Printf("  Requests: %d total, %d success, %d failures\n",
		metrics.TotalRequests, metrics.SuccessCount, metrics.FailureCount)

	// Latency metrics
	min, max, avg := metrics.GetMinMaxAvg()
	p95 := metrics.GetPercentile(0.95)
	p99 := metrics.GetPercentile(0.99)

	fmt.Printf("  Latency:\n")
	fmt.Printf("    Min: %s\n", min.Round(time.Millisecond))
	fmt.Printf("    Avg: %s\n", avg.Round(time.Millisecond))
	fmt.Printf("    Max: %s\n", max.Round(time.Millisecond))
	fmt.Printf("    P95: %s\n", p95.Round(time.Millisecond))
	fmt.Printf("    P99: %s\n", p99.Round(time.Millisecond))

	// Overall result
	if result.Passed {
		fmt.Printf("  Result: ✓ PASSED\n")
	} else {
		fmt.Printf("  Result: ✗ FAILED\n")
	}
}
