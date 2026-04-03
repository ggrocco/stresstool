package protocol

import (
	"fmt"
	"time"

	"stresstool/internal/config"
	payloadpb "stresstool/internal/protocol/payloadpb/api/v1"
	"stresstool/internal/runner"
)

// ConfigToProto converts a config.Config to protobuf.
func ConfigToProto(c *config.Config) *payloadpb.Config {
	if c == nil {
		return nil
	}
	out := &payloadpb.Config{
		Funcs: make([]*payloadpb.FuncDef, 0, len(c.Funcs)),
		Tests: make([]*payloadpb.Test, 0, len(c.Tests)),
	}
	for _, f := range c.Funcs {
		out.Funcs = append(out.Funcs, &payloadpb.FuncDef{
			Name: f.Name,
			Cmd:  append([]string(nil), f.Cmd...),
		})
	}
	for _, t := range c.Tests {
		out.Tests = append(out.Tests, testToProto(&t))
	}
	return out
}

func testToProto(t *config.Test) *payloadpb.Test {
	if t == nil {
		return nil
	}
	pb := &payloadpb.Test{
		Name:              t.Name,
		Path:              t.Path,
		Method:            t.Method,
		RequestsPerSecond: int32(t.RequestsPerSecond),
		Threads:           int32(t.Threads),
		RunSeconds:        int32(t.RunSeconds),
		Headers:           map[string]string{},
		Body:              t.Body,
		Nodes:             map[string]*payloadpb.NodeOverride{},
	}
	for k, v := range t.Headers {
		pb.Headers[k] = v
	}
	if t.Assert != nil {
		pb.Assert = &payloadpb.Assertion{
			StatusCode:    int32(t.Assert.StatusCode),
			BodyContains:  t.Assert.BodyContains,
			BodyEquals:    t.Assert.BodyEquals,
			BodyNotEquals: t.Assert.BodyNotEquals,
			MaxLatencyMs:  int32(t.Assert.MaxLatencyMs),
		}
	}
	for name, n := range t.Nodes {
		pb.Nodes[name] = &payloadpb.NodeOverride{
			RequestsPerSecond: int32(n.RequestsPerSecond),
			Threads:           int32(n.Threads),
		}
	}
	return pb
}

// ConfigFromProto builds config.Config from protobuf.
func ConfigFromProto(pb *payloadpb.Config) (*config.Config, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil config")
	}
	out := &config.Config{
		Funcs: make([]config.FuncDef, 0, len(pb.Funcs)),
		Tests: make([]config.Test, 0, len(pb.Tests)),
	}
	for _, f := range pb.Funcs {
		if f == nil {
			continue
		}
		out.Funcs = append(out.Funcs, config.FuncDef{
			Name: f.Name,
			Cmd:  append([]string(nil), f.Cmd...),
		})
	}
	for _, t := range pb.Tests {
		if t == nil {
			continue
		}
		ct, err := testFromProto(t)
		if err != nil {
			return nil, err
		}
		out.Tests = append(out.Tests, *ct)
	}
	return out, nil
}

func testFromProto(t *payloadpb.Test) (*config.Test, error) {
	if t == nil {
		return nil, fmt.Errorf("nil test")
	}
	out := &config.Test{
		Name:              t.Name,
		Path:              t.Path,
		Method:            t.Method,
		RequestsPerSecond: int(t.RequestsPerSecond),
		Threads:           int(t.Threads),
		RunSeconds:        int(t.RunSeconds),
		Headers:           map[string]string{},
		Body:              t.Body,
		Nodes:             map[string]config.Node{},
	}
	for k, v := range t.Headers {
		out.Headers[k] = v
	}
	if t.Assert != nil {
		out.Assert = &config.Assertion{
			StatusCode:    int(t.Assert.StatusCode),
			BodyContains:  t.Assert.BodyContains,
			BodyEquals:    t.Assert.BodyEquals,
			BodyNotEquals: t.Assert.BodyNotEquals,
			MaxLatencyMs:  int(t.Assert.MaxLatencyMs),
		}
	}
	for name, n := range t.Nodes {
		if n == nil {
			continue
		}
		out.Nodes[name] = config.Node{
			RequestsPerSecond: int(n.RequestsPerSecond),
			Threads:           int(n.Threads),
		}
	}
	return out, nil
}

// TestResultToProto converts runner.TestResult to protobuf TestResult.
func TestResultToProto(r *runner.TestResult) *payloadpb.TestResult {
	if r == nil {
		return nil
	}
	pb := &payloadpb.TestResult{
		Test:         testToProto(r.Test),
		Metrics:      metricsToProto(r.Metrics),
		Passed:       r.Passed,
		Errors:       append([]string(nil), r.Errors...),
		StoppedEarly: r.StoppedEarly,
	}
	if r.Assertions != nil {
		pb.Assertions = &payloadpb.Assertions{}
	}
	return pb
}

func metricsToProto(m *runner.Metrics) *payloadpb.Metrics {
	if m == nil {
		return nil
	}
	pb := &payloadpb.Metrics{
		TotalRequests:     m.TotalRequests,
		SuccessCount:      m.SuccessCount,
		FailureCount:      m.FailureCount,
		AssertionFailures: m.AssertionFailures,
		LatenciesNanos:    make([]int64, 0, len(m.Latencies)),
		StatusCodes:       map[int32]int64{},
		Errors:            map[string]int64{},
	}
	for _, d := range m.Latencies {
		pb.LatenciesNanos = append(pb.LatenciesNanos, d.Nanoseconds())
	}
	for code, n := range m.StatusCodes {
		pb.StatusCodes[int32(code)] = n
	}
	for msg, n := range m.Errors {
		pb.Errors[msg] = n
	}
	return pb
}

// TestResultFromProto builds runner.TestResult from protobuf (Assertions is nil).
func TestResultFromProto(pb *payloadpb.TestResult) (*runner.TestResult, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil test result")
	}
	t, err := testFromProto(pb.Test)
	if err != nil {
		return nil, err
	}
	m, err := metricsFromProto(pb.Metrics)
	if err != nil {
		return nil, err
	}
	return &runner.TestResult{
		Test:         t,
		Metrics:      m,
		Assertions:   nil,
		Passed:       pb.Passed,
		Errors:       append([]string(nil), pb.Errors...),
		StoppedEarly: pb.StoppedEarly,
	}, nil
}

func metricsFromProto(pb *payloadpb.Metrics) (*runner.Metrics, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil metrics")
	}
	latencies := make([]time.Duration, 0, len(pb.LatenciesNanos))
	for _, ns := range pb.LatenciesNanos {
		latencies = append(latencies, durationFromNanos(ns))
	}
	statusCodes := make(map[int]int64, len(pb.StatusCodes))
	for code, n := range pb.StatusCodes {
		statusCodes[int(code)] = n
	}
	errors := make(map[string]int64, len(pb.Errors))
	for msg, n := range pb.Errors {
		errors[msg] = n
	}
	return runner.NewMetricsSnapshot(
		pb.TotalRequests,
		pb.SuccessCount,
		pb.FailureCount,
		pb.AssertionFailures,
		latencies,
		statusCodes,
		errors,
	), nil
}

func durationFromNanos(ns int64) time.Duration {
	// Protobuf latencies are int64 nanoseconds; guard overflow for very large values.
	if ns < 0 {
		return 0
	}
	return time.Duration(ns)
}
