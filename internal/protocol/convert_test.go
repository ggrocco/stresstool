package protocol

import (
	"testing"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/runner"
)

func TestConfigRoundTrip(t *testing.T) {
	cfg := &config.Config{
		Funcs: []config.FuncDef{{Name: "x", Cmd: []string{"echo", "hi"}}},
		Tests: []config.Test{{
			Name: "t1", Path: "/p", Method: "GET",
			RequestsPerSecond: 10, Threads: 2, RunSeconds: 5,
			Headers: map[string]string{"H": "v"},
			Body:    "{}",
			Assert:  &config.Assertion{StatusCode: 200, MaxLatencyMs: 100},
			Nodes:   map[string]config.Node{"n1": {RequestsPerSecond: 5, Threads: 1}},
		}},
	}
	pb := ConfigToProto(cfg)
	out, err := ConfigFromProto(pb)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Funcs) != 1 || out.Funcs[0].Name != "x" {
		t.Fatalf("funcs: %+v", out.Funcs)
	}
	if len(out.Tests) != 1 || out.Tests[0].Name != "t1" || out.Tests[0].RequestsPerSecond != 10 {
		t.Fatalf("tests: %+v", out.Tests)
	}
	if out.Tests[0].Assert == nil || out.Tests[0].Assert.StatusCode != 200 {
		t.Fatal("assert round-trip")
	}
	if len(out.Tests[0].Nodes) != 1 {
		t.Fatal("nodes round-trip")
	}
}

func TestTestResultRoundTrip(t *testing.T) {
	ct := &config.Test{Name: "t", Path: "/x", Method: "GET", RequestsPerSecond: 1, Threads: 1, RunSeconds: 1}
	m := runner.NewMetricsSnapshot(3, 2, 1, 0,
		[]time.Duration{time.Millisecond, 2 * time.Millisecond},
		map[int]int64{200: 2, 500: 1},
		map[string]int64{"err": 1},
	)
	res := &runner.TestResult{
		Test:         ct,
		Metrics:      m,
		Passed:       false,
		Errors:       []string{"e1"},
		StoppedEarly: true,
	}
	pb := TestResultToProto(res)
	out, err := TestResultFromProto(pb)
	if err != nil {
		t.Fatal(err)
	}
	if out.Test.Name != "t" || !out.StoppedEarly || len(out.Errors) != 1 {
		t.Fatalf("result: %+v", out)
	}
	if out.Metrics.TotalRequests != 3 || len(out.Metrics.Latencies) != 2 {
		t.Fatalf("metrics: %+v", out.Metrics)
	}
}
