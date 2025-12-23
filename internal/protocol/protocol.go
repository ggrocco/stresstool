package protocol

import (
	"stresstool/internal/config"
	"stresstool/internal/runner"
)

// MessageType defines the type of message being sent
type MessageType string

const (
	// Node -> Controller: Initial connection
	MsgTypeHello MessageType = "hello"
	// Controller -> Node: Send test specification
	MsgTypeTestSpec MessageType = "test_spec"
	// Node -> Controller: Progress update during test execution
	MsgTypeProgress MessageType = "progress"
	// Node -> Controller: Final test result
	MsgTypeTestResult MessageType = "test_result"
	// Controller -> Node: Request to start executing tests
	MsgTypeStartTests MessageType = "start_tests"
	// Node -> Controller: Acknowledgment of ready state
	MsgTypeReady MessageType = "ready"
	// Controller -> All Nodes: Signal that all tests are complete
	MsgTypeComplete MessageType = "complete"
)

// Message is the base message structure
type Message struct {
	Type MessageType `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// HelloMessage is sent by nodes when they connect
type HelloMessage struct {
	NodeName string `json:"node_name"`
	Version  string `json:"version"`
}

// TestSpecMessage contains the test configuration to execute
type TestSpecMessage struct {
	Config *config.Config `json:"config"`
	// NodeName tells the node which overrides to apply
	NodeName string `json:"node_name"`
	// Parallel indicates if tests should run in parallel
	Parallel bool `json:"parallel"`
}

// ProgressMessage contains progress updates during test execution
type ProgressMessage struct {
	NodeName string  `json:"node_name"`
	TestName string  `json:"test_name"`
	Elapsed  float64 `json:"elapsed_seconds"`
	Total    int64   `json:"total_requests"`
	Failures int64   `json:"failures"`
	RPS      float64 `json:"rps"`
	Done     bool    `json:"done"`
}

// TestResultMessage contains the final result of a test
type TestResultMessage struct {
	NodeName string             `json:"node_name"`
	TestName string             `json:"test_name"`
	Result   *runner.TestResult `json:"result"`
}

// ReadyMessage indicates node is ready to receive tests
type ReadyMessage struct {
	NodeName string `json:"node_name"`
}

// StartTestsMessage signals nodes to begin execution
type StartTestsMessage struct {
	Timestamp int64 `json:"timestamp"`
}
