package cli

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/protocol"
	payloadpb "stresstool/internal/protocol/payloadpb/api/v1"
	"stresstool/internal/runner"
)

//go:embed web
var webFS embed.FS

// controllerEventKind identifies the type of a state mutation event
type controllerEventKind int

const (
	evNodeConnected controllerEventKind = iota
	evNodeDisconnected
	evProgress
	evTestResult
	evNodeError
	evSetExpected
	evNodeReady
	evBeginAwaitReady
	evClearAwaitReady
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
	awaitNodes      []string
	awaitReply      chan struct{}
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
	uiAddr     string
	configFile string
	config     *config.Config
	parallel   bool
	verbose    bool
	tlsOpts    TLSOptions

	// eventChan serialises all state mutations through the state manager
	eventChan chan controllerEvent
	// queryChan allows the main goroutine to read state synchronously
	queryChan chan stateQuery
	// completionChan is closed by the state manager when all tests have results
	completionChan chan struct{}
	// quitChan asks the state manager to exit
	quitChan chan struct{}
	// triggerChan receives a signal from the UI to start tests
	triggerChan chan struct{}

	logChan      chan string
	logQueryChan chan logQuery
}

// NodeConnection represents a connected node (gRPC session).
type NodeConnection struct {
	Name     string
	PeerAddr string
	SendCh   chan<- *payloadpb.ControllerMessage
}

type logQuery struct {
	offset    int
	replyChan chan logQueryReply
}

type logQueryReply struct {
	lines []string
	total int
}

// log writes a message to both stdout and the internal log buffer for the web UI.
func (c *Controller) log(format string, args ...interface{}) {
	line := fmt.Sprintf(format, args...)
	fmt.Println(line)
	c.logChan <- line
}

// runLogManager owns the log buffer and serves queries via channels.
func (c *Controller) runLogManager() {
	var buf []string
	for {
		select {
		case <-c.quitChan:
			return
		case line := <-c.logChan:
			buf = append(buf, line)
		case q := <-c.logQueryChan:
			offset := q.offset
			if offset > len(buf) {
				offset = len(buf)
			}
			cp := make([]string, len(buf[offset:]))
			copy(cp, buf[offset:])
			q.replyChan <- logQueryReply{lines: cp, total: len(buf)}
		}
	}
}

// RunController starts the controller and waits for nodes to connect.
func RunController(listenAddr, configFile, uiAddr string, parallel, verbose bool, tlsOpts TLSOptions) error {
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
		uiAddr:         uiAddr,
		configFile:     configFile,
		config:         cfg,
		parallel:       parallel,
		verbose:        verbose,
		tlsOpts:        tlsOpts,
		eventChan:      make(chan controllerEvent, 100),
		queryChan:      make(chan stateQuery),
		completionChan: make(chan struct{}),
		quitChan:       make(chan struct{}),
		triggerChan:    make(chan struct{}, 1),
		logChan:        make(chan string, 100),
		logQueryChan:   make(chan logQuery),
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

	var awaitingReady map[string]struct{}
	var awaitReadyReply chan struct{}

	signalAwaitIfEmpty := func() {
		if awaitReadyReply != nil && len(awaitingReady) == 0 {
			select {
			case awaitReadyReply <- struct{}{}:
			default:
			}
			awaitReadyReply = nil
			awaitingReady = nil
		}
	}

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
				c.log("✓ Node connected: %s", event.nodeName)

			case evNodeDisconnected:
				delete(nodes, event.nodeName)
				if awaitingReady != nil {
					delete(awaitingReady, event.nodeName)
					signalAwaitIfEmpty()
				}
				c.log("✗ Node disconnected: %s", event.nodeName)

			case evProgress:
				p := event.progress
				if _, ok := progress[p.NodeName]; !ok {
					progress[p.NodeName] = make(map[string]*protocol.ProgressMessage)
				}
				progress[p.NodeName][p.TestName] = p

				// Log progress to web UI
				prefix := fmt.Sprintf("%s / %s", p.NodeName, p.TestName)
				if p.Done {
					c.log("✓ %s: COMPLETE - %d requests, %.1f RPS, %d failures",
						prefix, p.Total, p.RPS, p.Failures)
				} else {
					c.log("→ %s: %.0fs elapsed - %d requests, %.1f RPS, %d failures",
						prefix, p.Elapsed, p.Total, p.RPS, p.Failures)
				}

				// Redraw progress lines in terminal
				if lineCount > 0 {
					fmt.Printf("\033[%dA", lineCount)
				}
				lineCount = 0
				for nodeName, nodeProgress := range progress {
					for _, pr := range nodeProgress {
						termPrefix := fmt.Sprintf("%s / %s", nodeName, pr.TestName)
						if pr.Done {
							fmt.Printf("✓ %s: COMPLETE - %d requests, %.1f RPS, %d failures\n",
								termPrefix, pr.Total, pr.RPS, pr.Failures)
						} else {
							fmt.Printf("→ %s: %.0fs elapsed - %d requests, %.1f RPS, %d failures\n",
								termPrefix, pr.Elapsed, pr.Total, pr.RPS, pr.Failures)
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
				if awaitingReady != nil {
					delete(awaitingReady, event.nodeErr.NodeName)
					signalAwaitIfEmpty()
				}
				c.log("✗ Error from node %s during %s: %s",
					event.nodeErr.NodeName, event.nodeErr.Phase, event.nodeErr.Error)
				checkCompletion()

			case evSetExpected:
				expectedResults = event.expectedResults
				checkCompletion()

			case evNodeReady:
				if awaitingReady != nil {
					delete(awaitingReady, event.nodeName)
					signalAwaitIfEmpty()
				}
				if c.verbose {
					fmt.Printf("Node %s is ready\n", event.nodeName)
				}

			case evBeginAwaitReady:
				awaitingReady = make(map[string]struct{}, len(event.awaitNodes))
				for _, name := range event.awaitNodes {
					awaitingReady[name] = struct{}{}
				}
				awaitReadyReply = event.awaitReply
				signalAwaitIfEmpty()

			case evClearAwaitReady:
				awaitingReady = nil
				awaitReadyReply = nil
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
	go c.runStateManager()
	go c.runLogManager()

	fmt.Printf("Controller listening on %s (gRPC)\n", c.listenAddr)
	fmt.Printf("Loaded config with %d test(s)\n", len(c.config.Tests))
	fmt.Println("\nWaiting for nodes to connect...")

	if c.uiAddr != "" {
		go c.startUIServer()
		fmt.Printf("UI available at http://%s — use the web interface to check nodes and start tests\n", c.uiAddr)
		fmt.Println("Press Ctrl+C to cancel, or type 'start' / 'nodes' in this terminal as well.")
	} else {
		fmt.Println("Press Ctrl+C to cancel, or wait for nodes and then type 'start' to begin tests")
	}

	return listenAndServeGRPC(c, c.tlsOpts)
}

func (c *Controller) runControllerLoop() error {
	inputChan := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputChan <- scanner.Text()
		}
	}()

	fmt.Println("\nType 'start' when ready to begin tests, or 'nodes' to see connected nodes:")

	for {
		select {
		case input := <-inputChan:
			switch input {
			case "start":
				return c.startTests()
			case "nodes":
				c.printConnectedNodes()
			default:
				fmt.Println("Unknown command. Type 'start' to begin or 'nodes' to list connected nodes.")
			}
		case <-c.triggerChan:
			c.log("Start triggered via UI...")
			return c.startTests()
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

	c.log("Starting tests on %d node(s)...", len(nodes))

	// Build expected results map and tell the state manager
	expected := make(map[string]map[string]bool, len(nodes))
	for nodeName := range nodes {
		expected[nodeName] = make(map[string]bool, len(c.config.Tests))
		for _, test := range c.config.Tests {
			expected[nodeName][test.Name] = false
		}
	}
	c.eventChan <- controllerEvent{kind: evSetExpected, expectedResults: expected}

	awaitReply := make(chan struct{}, 1)
	nodeNames := make([]string, 0, len(nodes))
	for name := range nodes {
		nodeNames = append(nodeNames, name)
	}
	c.eventChan <- controllerEvent{
		kind:       evBeginAwaitReady,
		awaitNodes: nodeNames,
		awaitReply: awaitReply,
	}

	pbCfg := protocol.ConfigToProto(c.config)
	for nodeName, node := range nodes {
		msg := &payloadpb.ControllerMessage{
			Payload: &payloadpb.ControllerMessage_TestSpec{
				TestSpec: &payloadpb.TestSpecMessage{
					Config:   pbCfg,
					NodeName: nodeName,
					Parallel: c.parallel,
				},
			},
		}
		select {
		case node.SendCh <- msg:
		default:
			fmt.Printf("Failed to enqueue test spec for %s (send buffer full)\n", nodeName)
		}
	}

	timer := time.NewTimer(2 * time.Minute)
	select {
	case <-awaitReply:
	case <-timer.C:
		c.log("Timeout waiting for nodes to report ready; sending start anyway")
		c.eventChan <- controllerEvent{kind: evClearAwaitReady}
	}
	timer.Stop()

	for nodeName, node := range nodes {
		startMsg := &payloadpb.ControllerMessage{
			Payload: &payloadpb.ControllerMessage_StartTests{
				StartTests: &payloadpb.StartTestsMessage{Timestamp: time.Now().Unix()},
			},
		}
		select {
		case node.SendCh <- startMsg:
		default:
			fmt.Printf("Failed to enqueue start signal for %s\n", nodeName)
		}
	}

	// Block until the state manager signals completion (event-driven, no polling)
	<-c.completionChan

	// Print final summary (also moves terminal cursor down past progress lines)
	c.printFinalSummary()

	for nodeName, node := range nodes {
		completeMsg := &payloadpb.ControllerMessage{
			Payload: &payloadpb.ControllerMessage_Complete{Complete: &payloadpb.CompleteMessage{}},
		}
		select {
		case node.SendCh <- completeMsg:
		default:
			fmt.Printf("Failed to enqueue complete signal for %s\n", nodeName)
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

// startUIServer starts an HTTP server that provides a web UI for triggering tests.
func (c *Controller) startUIServer() {
	webContent, _ := fs.Sub(webFS, "web")
	staticFS := http.FileServer(http.FS(webContent))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		f, err := webContent.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.Handle("/static/", http.StripPrefix("/static/", staticFS))
	mux.HandleFunc("/api/nodes", c.handleAPINodes)
	mux.HandleFunc("/api/start", c.handleAPIStart)
	mux.HandleFunc("/api/stop", c.handleAPIStop)
	mux.HandleFunc("/api/config", c.handleAPIConfig)
	mux.HandleFunc("/api/logs", c.handleAPILogs)

	if err := http.ListenAndServe(c.uiAddr, mux); err != nil {
		fmt.Printf("UI server error: %v\n", err)
	}
}

func (c *Controller) handleAPINodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodes := c.queryNodes()
	type nodeInfo struct {
		Name string `json:"name"`
		Addr string `json:"addr"`
	}
	infos := make([]nodeInfo, 0, len(nodes))
	for name, nc := range nodes {
		addr := ""
		if nc.PeerAddr != "" {
			host, _, _ := net.SplitHostPort(nc.PeerAddr)
			if host == "" {
				host = nc.PeerAddr
			}
			ip := net.ParseIP(host)
			if ip != nil && ip.IsLoopback() {
				addr = "local"
			} else {
				addr = host
			}
		}
		infos = append(infos, nodeInfo{Name: name, Addr: addr})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"nodes": infos})
}

func (c *Controller) handleAPIStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodes := c.queryNodes()
	for _, nc := range nodes {
		stopMsg := &payloadpb.ControllerMessage{
			Payload: &payloadpb.ControllerMessage_StopTests{
				StopTests: &payloadpb.StopTestsMessage{Reason: "user_stop"},
			},
		}
		select {
		case nc.SendCh <- stopMsg:
		default:
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "nodes": len(nodes)})
}

func (c *Controller) handleAPIStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case c.triggerChan <- struct{}{}:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "start already triggered or tests already running"})
	}
}

func (c *Controller) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := os.ReadFile(c.configFile)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

func (c *Controller) handleAPILogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &offset)
	}
	reply := make(chan logQueryReply, 1)
	c.logQueryChan <- logQuery{offset: offset, replyChan: reply}
	res := <-reply
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"lines": res.lines,
		"total": res.total,
	})
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
	if result.StoppedEarly {
		fmt.Printf("  Note: run stopped early (partial result)\n")
	}
	if result.Passed {
		fmt.Printf("  Result: ✓ PASSED\n")
	} else {
		fmt.Printf("  Result: ✗ FAILED\n")
	}
}
