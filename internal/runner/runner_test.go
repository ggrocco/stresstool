package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

// newTestRunner returns a Runner backed by a fresh Evaluator (closed via t.Cleanup).
func newTestRunner(t *testing.T, cfg *config.Config) *Runner {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	eval := placeholders.NewEvaluator(cfg)
	t.Cleanup(func() { eval.Close() })
	return NewRunner(eval, false, nil)
}

// runTest runs a test and collects all progress updates.
// The progress reporter goroutine is guaranteed to have exited when RunTest
// returns, so closing progressChan here is race-free.
// It returns the result and the final Done progress update.
func runTest(r *Runner, test *config.Test) (*TestResult, ProgressUpdate) {
	progressChan := make(chan ProgressUpdate, 200)
	result := r.RunTest(context.Background(), test, progressChan)
	close(progressChan)

	// Drain and find the Done update
	var last ProgressUpdate
	for u := range progressChan {
		last = u
	}
	return result, last
}

// --- helpers for building minimal test configs ---

func makeTest(serverURL, name string, rps, threads, seconds int) *config.Test {
	return &config.Test{
		Name:              name,
		Path:              serverURL,
		Method:            "GET",
		RequestsPerSecond: rps,
		Threads:           threads,
		RunSeconds:        seconds,
	}
}

// ============================================================
// Integration tests using httptest.Server
// ============================================================

func TestRunTest_BasicGET(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "basic-get", 5, 2, 1)

	result, final := runTest(r, test)

	if !result.Passed {
		t.Errorf("expected test to pass")
	}
	if result.Metrics.TotalRequests == 0 {
		t.Errorf("expected at least one request to be made")
	}
	if result.Metrics.SuccessCount != result.Metrics.TotalRequests {
		t.Errorf("expected all requests to succeed: total=%d success=%d",
			result.Metrics.TotalRequests, result.Metrics.SuccessCount)
	}
	if result.Metrics.FailureCount != 0 {
		t.Errorf("expected no failures, got %d", result.Metrics.FailureCount)
	}
	if !final.Done {
		t.Errorf("expected final progress update to be marked Done")
	}
	if final.Total != result.Metrics.TotalRequests {
		t.Errorf("progress total %d != metrics total %d", final.Total, result.Metrics.TotalRequests)
	}
}

func TestRunTest_StatusCodeAssertion_Pass(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "assert-status", 5, 1, 1)
	test.Assert = &config.Assertion{StatusCode: 201}

	result, _ := runTest(r, test)

	if !result.Passed {
		t.Errorf("expected test to pass with 201 assertion")
	}
	if result.Metrics.AssertionFailures != 0 {
		t.Errorf("expected no assertion failures, got %d", result.Metrics.AssertionFailures)
	}
}

func TestRunTest_StatusCodeAssertion_Fail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // returns 200, but test expects 201
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "assert-status-fail", 5, 1, 1)
	test.Assert = &config.Assertion{StatusCode: 201}

	result, _ := runTest(r, test)

	if result.Passed {
		t.Errorf("expected test to fail when status code assertion is not met")
	}
}

func TestRunTest_BodyContainsAssertion_Pass(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status":"healthy"}`)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "assert-body-pass", 5, 1, 1)
	test.Assert = &config.Assertion{
		StatusCode:   200,
		BodyContains: "healthy",
	}

	result, _ := runTest(r, test)

	if !result.Passed {
		t.Errorf("expected test to pass with body_contains assertion")
	}
}

func TestRunTest_BodyContainsAssertion_Fail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status":"degraded"}`)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "assert-body-fail", 5, 1, 1)
	test.Assert = &config.Assertion{
		StatusCode:   200,
		BodyContains: "healthy",
	}

	result, _ := runTest(r, test)

	if result.Passed {
		t.Errorf("expected test to fail when body does not contain expected string")
	}
}

func TestRunTest_MaxLatencyAssertion_Pass(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "latency-pass", 5, 1, 1)
	test.Assert = &config.Assertion{MaxLatencyMs: 5000} // 5 second limit — easy to meet

	result, _ := runTest(r, test)

	if !result.Passed {
		t.Errorf("expected test to pass with generous latency assertion")
	}
}

func TestRunTest_MaxLatencyAssertion_Fail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "latency-fail", 5, 1, 1)
	test.Assert = &config.Assertion{MaxLatencyMs: 1} // 1ms — impossible to meet

	result, _ := runTest(r, test)

	if result.Passed {
		t.Errorf("expected test to fail when latency exceeds max")
	}
}

func TestRunTest_ServerErrors_RecordedAsFailures(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "server-errors", 5, 2, 1)

	result, _ := runTest(r, test)

	if result.Metrics.FailureCount == 0 {
		t.Errorf("expected failures for 500 responses")
	}
	if result.Metrics.StatusCodes[500] == 0 {
		t.Errorf("expected 500 status codes to be tracked")
	}
}

func TestRunTest_UnreachableServer_RecordsErrors(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, nil)
	test := makeTest("http://127.0.0.1:1", "unreachable", 5, 1, 1)

	result, _ := runTest(r, test)

	if result.Metrics.TotalRequests == 0 {
		t.Errorf("expected at least one request attempt")
	}
	if len(result.Metrics.Errors) == 0 {
		t.Errorf("expected errors to be recorded for unreachable server")
	}
}

func TestRunTest_POSTWithBody(t *testing.T) {
	t.Parallel()
	var receivedBody atomic.Value

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			receivedBody.Store(payload)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := &config.Test{
		Name:              "post-body",
		Path:              srv.URL,
		Method:            "POST",
		RequestsPerSecond: 5,
		Threads:           1,
		RunSeconds:        1,
		Headers:           map[string]string{"Content-Type": "application/json"},
		Body:              `{"key":"value"}`,
		Assert:            &config.Assertion{StatusCode: 200},
	}

	result, _ := runTest(r, test)

	if !result.Passed {
		t.Errorf("expected POST test to pass")
	}
	payload, ok := receivedBody.Load().(map[string]string)
	if !ok || payload["key"] != "value" {
		t.Errorf("expected server to receive body {key: value}, got: %v", receivedBody.Load())
	}
}

func TestRunTest_WithPlaceholderInHeader(t *testing.T) {
	t.Parallel()
	var receivedHeader atomic.Value

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader.Store(r.Header.Get("X-Request-ID"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := &config.Test{
		Name:              "placeholder-header",
		Path:              srv.URL,
		Method:            "GET",
		RequestsPerSecond: 5,
		Threads:           1,
		RunSeconds:        1,
		Headers:           map[string]string{"X-Request-ID": "{{uuid()}}"},
		Assert:            &config.Assertion{StatusCode: 200},
	}

	result, _ := runTest(r, test)

	if !result.Passed {
		t.Errorf("expected test with placeholder header to pass")
	}
	header, _ := receivedHeader.Load().(string)
	if len(header) == 0 {
		t.Errorf("expected X-Request-ID header to be set")
	}
	// Should look like a UUID (has dashes)
	if len(header) != 36 {
		t.Errorf("expected UUID-length header value, got %q (len=%d)", header, len(header))
	}
}

func TestRunTest_RateLimit_RespectsRPS(t *testing.T) {
	t.Parallel()
	var requestCount int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	// 10 RPS for 2 seconds = ~20 requests, with some tolerance
	test := makeTest(srv.URL, "rate-limit", 10, 2, 2)

	runTest(r, test)

	count := atomic.LoadInt64(&requestCount)
	// Allow ±50% tolerance to avoid flaky tests under load
	if count < 5 || count > 40 {
		t.Errorf("expected ~20 requests at 10 RPS for 2s, got %d", count)
	}
}

func TestRunTest_ProgressUpdates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "progress-check", 5, 1, 2)

	progressChan := make(chan ProgressUpdate, 200)
	var updates []ProgressUpdate
	done := make(chan struct{})

	go func() {
		defer close(done)
		for u := range progressChan {
			updates = append(updates, u)
		}
	}()

	r.RunTest(context.Background(), test, progressChan)
	close(progressChan)
	<-done

	if len(updates) == 0 {
		t.Fatal("expected at least one progress update")
	}

	last := updates[len(updates)-1]
	if !last.Done {
		t.Errorf("expected last progress update to be marked Done")
	}
	if last.TestName != "progress-check" {
		t.Errorf("expected TestName='progress-check', got %q", last.TestName)
	}
}

func TestRunTest_Warmup_RampsUp(t *testing.T) {
	t.Parallel()
	var requestCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	// 20 RPS, 2s warmup, 2s steady.
	// Without warmup we'd expect ~80 requests (20*4). With a linear ramp
	// during the first 2s we expect the warmup to contribute ~20 (≈20*2/2),
	// plus ~40 from the steady phase — ≈60 total.
	test := makeTest(srv.URL, "warmup-ramp", 20, 2, 2)
	test.WarmupSeconds = 2

	start := time.Now()
	result, _ := runTest(r, test)
	elapsed := time.Since(start)

	count := atomic.LoadInt64(&requestCount)
	if count == 0 {
		t.Fatal("expected some requests during warmup + run")
	}
	// Upper bound: the total duration is 4s, so even at full RPS we'd expect
	// ~80. Assert meaningfully fewer requests than the steady-state maximum
	// over the full window, proving warmup throttled the early phase.
	if count >= 80 {
		t.Errorf("expected ramp-up to reduce total requests below steady-state 80, got %d", count)
	}
	// Sanity: the run took at least warmup + run_seconds.
	if elapsed < 4*time.Second-200*time.Millisecond {
		t.Errorf("test finished too early: %v", elapsed)
	}
	if !result.Passed {
		t.Errorf("expected warmup test to pass")
	}
}

func TestRunTest_MetricsConsistency(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestRunner(t, nil)
	test := makeTest(srv.URL, "consistency", 10, 3, 1)

	result, _ := runTest(r, test)
	m := result.Metrics

	if m.SuccessCount+m.FailureCount != m.TotalRequests {
		t.Errorf("SuccessCount(%d) + FailureCount(%d) != TotalRequests(%d)",
			m.SuccessCount, m.FailureCount, m.TotalRequests)
	}
	if int64(len(m.Latencies)) != m.TotalRequests {
		t.Errorf("len(Latencies)=%d != TotalRequests=%d", len(m.Latencies), m.TotalRequests)
	}
}
