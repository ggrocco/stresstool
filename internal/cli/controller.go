package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/protocol"
	"stresstool/internal/runner"
)

// controllerEventKind identifies the type of a state mutation event
type controllerEventKind int

const (
	evNodeConnected controllerEventKind = iota
	evNodeDisconnected
	evProgress
	evTestResult
	evNodeError
	evSetExpected
)

// controllerEvent carries a state mutation to the state manager goroutine
type controllerEvent struct {
	kind            controllerEventKind
	nodeName        string
	conn            *NodeConnection
	progress        *protocol.ProgressMessage
	result          *protocol.TestResultMessage
	nodeErr         *protocol.ErrorMessage
	expectedResults map[string]map[string]bool
}

// queryKind identifies the type of a synchronous state query
type queryKind int

const (
	queryNodes queryKind = iota
	queryFinalState
)

// stateQuery is sent over queryChan to request a synchronous read from the state manager
type stateQuery struct {
	kind      queryKind
	replyChan chan stateQueryReply
}

// stateQueryReply carries the response to a stateQuery
type stateQueryReply struct {
	nodes      map[string]*NodeConnection
	results    map[string]map[string]*runner.TestResult
	nodeErrors map[string]*protocol.ErrorMessage
}

// Controller manages test execution across multiple nodes
type Controller struct {
	listenAddr string
	config     *config.Config
	parallel   bool
	verbose    bool

	// eventChan serialises all state mutations through the state manager
	eventChan chan controllerEvent
	// queryChan allows the main goroutine to read state synchronously
	queryChan chan stateQuery
	// completionChan is closed by the state manager when all tests have results
	completionChan chan struct{}
	// quitChan asks the state manager to exit
	quitChan chan struct{}
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
		listenAddr:     listenAddr,
		config:         cfg,
		parallel:       parallel,
		verbose:        verbose,
		eventChan:      make(chan controllerEvent, 100),
		queryChan:      make(chan stateQuery),
		completionChan: make(chan struct{}),
		quitChan:       make(chan struct{}),
	}

	return ctrl.start()
}

// runStateManager owns all shared maps and processes events/queries without any locks.
// It runs as a dedicated goroutine for the lifetime of the controller.
func (c *Controller) runStateManager() {
	nodes := make(map[string]*NodeConnection)
	progress := make(map[string]map[string]*protocol.ProgressMessage)
	results := make(map[string]map[string]*runner.TestResult)
	nodeErrors := make(map[string]*protocol.ErrorMessage)
	var lineCount int
	var expectedResults map[string]map[string]bool
	completionSignaled := false

	checkCompletion := func() {
		if completionSignaled || expectedResults == nil {
			return
		}
		if stateIsComplete(expectedResults, results, nodeErrors) {
			completionSignaled = true
			close(c.completionChan)
		}
	}

	for {
		select {
		case <-c.quitChan:
			return

		case event := <-c.eventChan:
			switch event.kind {
			case evNodeConnected:
				nodes[event.nodeName] = event.conn

			case evNodeDisconnected:
				delete(nodes, event.nodeName)
				fmt.Printf("✗ Node disconnected: %s\n", event.nodeName)

			case evProgress:
				p := event.progress
				if _, ok := progress[p.NodeName]; !ok {
					progress[p.NodeName] = make(map[string]*protocol.ProgressMessage)
				}
				progress[p.NodeName][p.TestName] = p

				// Redraw progress lines
				if lineCount > 0 {
					fmt.Printf("\033[%dA", lineCount)
				}
				lineCount = 0
				for nodeName, nodeProgress := range progress {
					for _, pr := range nodeProgress {
						prefix := fmt.Sprintf("%s / %s", nodeName, pr.TestName)
						if pr.Done {
							fmt.Printf("✓ %s: COMPLETE - %d requests, %.1f RPS, %d failures\n",
								prefix, pr.Total, pr.RPS, pr.Failures)
						} else {
							fmt.Printf("→ %s: %.0fs elapsed - %d requests, %.1f RPS, %d failures\n",
								prefix, pr.Elapsed, pr.Total, pr.RPS, pr.Failures)
						}
						lineCount++
					}
				}

			case evTestResult:
				r := event.result
				if _, ok := results[r.NodeName]; !ok {
					results[r.NodeName] = make(map[string]*runner.TestResult)
				}
				results[r.NodeName][r.TestName] = r.Result
				if c.verbose {
					fmt.Printf("Received result from %s for test %s\n", r.NodeName, r.TestName)
				}
				checkCompletion()

			case evNodeError:
				nodeErrors[event.nodeErr.NodeName] = event.nodeErr
				fmt.Printf("✗ Error from node %s during %s: %s\n",
					event.nodeErr.NodeName, event.nodeErr.Phase, event.nodeErr.Error)
				checkCompletion()

			case evSetExpected:
				expectedResults = event.expectedResults
				checkCompletion()
			}

		case query := <-c.queryChan:
			switch query.kind {
			case queryNodes:
				snapshot := make(map[string]*NodeConnection, len(nodes))
				for k, v := range nodes {
					snapshot[k] = v
				}
				query.replyChan <- stateQueryReply{nodes: snapshot}

			case queryFinalState:
				// Move cursor below progress display before returning state
				if lineCount > 0 {
					fmt.Printf("\033[%dB", lineCount)
				}
				resCopy := make(map[string]map[string]*runner.TestResult, len(results))
				for k, v := range results {
					resCopy[k] = v
				}
				errCopy := make(map[string]*protocol.ErrorMessage, len(nodeErrors))
				for k, v := range nodeErrors {
					errCopy[k] = v
				}
				query.replyChan <- stateQueryReply{results: resCopy, nodeErrors: errCopy}
			}
		}
	}
}

// stateIsComplete returns true when all non-failed nodes have reported results for all tests
func stateIsComplete(expected map[string]map[string]bool, results map[string]map[string]*runner.TestResult, nodeErrors map[string]*protocol.ErrorMessage) bool {
	for nodeName, tests := range expected {
		if _, failed := nodeErrors[nodeName]; failed {
			continue
		}
		nodeResults, ok := results[nodeName]
		if !ok {
			return false
		}
		for testName := range tests {
			if _, ok := nodeResults[testName]; !ok {
				return false
			}
		}
	}
	return true
}

// queryNodes asks the state manager for the current set of nodes
func (c *Controller) queryNodes() map[string]*NodeConnection {
	reply := make(chan stateQueryReply, 1)
	c.queryChan <- stateQuery{kind: queryNodes, replyChan: reply}
	return (<-reply).nodes
}

// queryFinalState asks the state manager for results and errors (moves cursor too)
func (c *Controller) queryFinalState() (map[string]map[string]*runner.TestResult, map[string]*protocol.ErrorMessage) {
	reply := make(chan stateQueryReply, 1)
	c.queryChan <- stateQuery{kind: queryFinalState, replyChan: reply}
	r := <-reply
	return r.results, r.nodeErrors
}

func (c *Controller) start() error {
	ln, err := net.Listen("tcp", c.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", c.listenAddr, err)
	}
	defer ln.Close()

	// Start the state manager goroutine
	go c.runStateManager()

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

	// Register node with state manager
	c.eventChan <- controllerEvent{
		kind:     evNodeConnected,
		nodeName: nodeName,
		conn: &NodeConnection{
			Name:    nodeName,
			Conn:    conn,
			Encoder: encoder,
			Decoder: decoder,
		},
	}

	// Listen for messages from this node
	c.handleNodeMessages(nodeName, decoder)

	// Notify state manager of disconnection
	c.eventChan <- controllerEvent{kind: evNodeDisconnected, nodeName: nodeName}
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
			c.eventChan <- controllerEvent{kind: evProgress, progress: progressData}

		case protocol.MsgTypeTestResult:
			resultData, err := parseMessageData[protocol.TestResultMessage](msg.Data)
			if err != nil {
				fmt.Printf("Failed to parse test result message: %v\n", err)
				continue
			}
			c.eventChan <- controllerEvent{kind: evTestResult, result: resultData}

		case protocol.MsgTypeReady:
			if c.verbose {
				fmt.Printf("Node %s is ready\n", nodeName)
			}

		case protocol.MsgTypeError:
			errorData, err := parseMessageData[protocol.ErrorMessage](msg.Data)
			if err != nil {
				fmt.Printf("Failed to parse error message: %v\n", err)
				continue
			}
			c.eventChan <- controllerEvent{kind: evNodeError, nodeErr: errorData}
		}
	}
}

func (c *Controller) printConnectedNodes() {
	nodes := c.queryNodes()
	fmt.Printf("\nConnected nodes (%d):\n", len(nodes))
	for nodeName := range nodes {
		fmt.Printf("  - %s\n", nodeName)
	}
	fmt.Println()
}

func (c *Controller) startTests() error {
	// Get a snapshot of current nodes from the state manager
	nodes := c.queryNodes()
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes connected")
	}

	fmt.Printf("\nStarting tests on %d node(s)...\n\n", len(nodes))

	// Build expected results map and tell the state manager
	expected := make(map[string]map[string]bool, len(nodes))
	for nodeName := range nodes {
		expected[nodeName] = make(map[string]bool, len(c.config.Tests))
		for _, test := range c.config.Tests {
			expected[nodeName][test.Name] = false
		}
	}
	c.eventChan <- controllerEvent{kind: evSetExpected, expectedResults: expected}

	// Send test spec to each node using the snapshot (no lock needed)
	for nodeName, node := range nodes {
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

	// Wait a bit for nodes to prepare
	time.Sleep(500 * time.Millisecond)

	// Send start signal to each node
	startMsg := protocol.Message{
		Type: protocol.MsgTypeStartTests,
		Data: protocol.StartTestsMessage{
			Timestamp: time.Now().Unix(),
		},
	}
	for nodeName, node := range nodes {
		if err := node.Encoder.Encode(startMsg); err != nil {
			fmt.Printf("Failed to send start signal to %s: %v\n", nodeName, err)
		}
	}

	// Block until the state manager signals completion (event-driven, no polling)
	<-c.completionChan

	// Print final summary (also moves terminal cursor down past progress lines)
	c.printFinalSummary()

	// Notify all nodes that the run is complete so they can disconnect
	completeMsg := protocol.Message{
		Type: protocol.MsgTypeComplete,
		Data: nil,
	}
	for nodeName, node := range nodes {
		if err := node.Encoder.Encode(completeMsg); err != nil {
			fmt.Printf("Failed to send complete signal to %s: %v\n", nodeName, err)
		}
	}

	// Stop the state manager
	c.quitChan <- struct{}{}

	return nil
}

func (c *Controller) printFinalSummary() {
	results, nodeErrors := c.queryFinalState()

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("DISTRIBUTED TEST SUMMARY")
	fmt.Println(strings.Repeat("=", 80))

	allPassed := true

	// Print results from successful nodes
	for nodeName, tests := range results {
		fmt.Printf("\n=== Node: %s ===\n", nodeName)
		for testName, result := range tests {
			fmt.Printf("\nTest: %s\n", testName)
			printTestResult(result)
			if !result.Passed {
				allPassed = false
			}
		}
	}

	// Print errors from failed nodes
	if len(nodeErrors) > 0 {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("NODE ERRORS")
		fmt.Println(strings.Repeat("=", 80))
		for nodeName, errorMsg := range nodeErrors {
			fmt.Printf("\n=== Node: %s ===\n", nodeName)
			fmt.Printf("Phase: %s\n", errorMsg.Phase)
			fmt.Printf("Error: %s\n", errorMsg.Error)
		}
		allPassed = false
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	if allPassed {
		fmt.Println("OVERALL RESULT: ✓ ALL TESTS PASSED")
	} else {
		fmt.Println("OVERALL RESULT: ✗ SOME TESTS FAILED")
	}
	fmt.Println(strings.Repeat("=", 80))
}

// Helper function to parse message data
func parseMessageData[T any](data interface{}) (*T, error) {
	// Convert via JSON to handle map[string]interface{} from decoder
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var result T
	if err := json.Unmarshal(bytes, &result); err != nil {
		return nil, err
	}

	return &result, nil
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

