package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"stresstool/internal/auth"
	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

// TestResult holds the result of running a test
type TestResult struct {
	Test       *config.Test
	Metrics    *Metrics
	Assertions *Assertions
	Passed     bool
	Errors     []string
	// StoppedEarly is true when the parent context was cancelled before the scheduled run duration ended.
	StoppedEarly bool
}

// Runner executes stress tests
type Runner struct {
	evaluator    *placeholders.Evaluator
	client       *http.Client
	verbose      bool
	authResolver *auth.Resolver
}

// NewRunner creates a new test runner
func NewRunner(evaluator *placeholders.Evaluator, verbose bool, authResolver *auth.Resolver) *Runner {
	transport := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Runner{
		evaluator: evaluator,
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		verbose:      verbose,
		authResolver: authResolver,
	}
}

// RunTest executes a single test. The parent ctx is used for cancellation (e.g. distributed stop signal);
// the run still ends at the configured deadline unless ctx is cancelled first.
func (r *Runner) RunTest(ctx context.Context, test *config.Test, progressChan chan<- ProgressUpdate) *TestResult {
	if ctx == nil {
		ctx = context.Background()
	}
	expectedTotal := test.RequestsPerSecond * test.RunSeconds
	metrics := NewMetrics(expectedTotal)
	assertions := NewAssertions(r.evaluator)
	result := &TestResult{
		Test:       test,
		Metrics:    metrics,
		Assertions: assertions,
		Passed:     true,
		Errors:     make([]string, 0),
	}

	startTime := time.Now()
	endTime := startTime.Add(time.Duration(test.RunSeconds) * time.Second)
	runCtx, cancelRun := context.WithDeadline(ctx, endTime)
	defer cancelRun()

	limiter := rate.NewLimiter(rate.Limit(float64(test.RequestsPerSecond)), 1)

	// Create worker pool
	var wg sync.WaitGroup

	// Progress reporting goroutine — tracked so RunTest can wait for it to exit
	// before returning, ensuring callers can safely close progressChan afterwards.
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				elapsed := time.Since(startTime)
				total := atomic.LoadInt64(&metrics.TotalRequests)
				failures := atomic.LoadInt64(&metrics.FailureCount)
				progressChan <- ProgressUpdate{
					TestName: test.Name,
					Elapsed:  elapsed,
					Total:    total,
					Failures: failures,
					RPS:      float64(total) / elapsed.Seconds(),
				}
			}
		}
	}()

	// Start workers
	for i := 0; i < test.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if err := limiter.Wait(runCtx); err != nil {
					return
				}
				r.executeRequest(test, metrics, assertions)
			}
		}()
	}

	<-runCtx.Done()
	wg.Wait()
	progressWg.Wait() // ensure progress reporter has exited
	metrics.Stop()    // drain and close the metrics channel before reading results

	if errors.Is(ctx.Err(), context.Canceled) {
		result.StoppedEarly = true
	}

	// Final progress update
	elapsed := time.Since(startTime)
	total := atomic.LoadInt64(&metrics.TotalRequests)
	failures := atomic.LoadInt64(&metrics.FailureCount)
	progressChan <- ProgressUpdate{
		TestName: test.Name,
		Elapsed:  elapsed,
		Total:    total,
		Failures: failures,
		RPS:      float64(total) / elapsed.Seconds(),
		Done:     true,
	}

	// Check if test passed
	if metrics.AssertionFailures > 0 || metrics.FailureCount > 0 {
		result.Passed = false
	}
	if result.StoppedEarly {
		result.Passed = false
	}

	return result
}

// executeRequest performs a single HTTP request
func (r *Runner) executeRequest(test *config.Test, metrics *Metrics, assertions *Assertions) {
	// Evaluate body
	bodyStr := test.Body
	if bodyStr != "" {
		var err error
		bodyStr, err = r.evaluator.Evaluate(bodyStr)
		if err != nil {
			errMsg := fmt.Sprintf("body placeholder evaluation failed: %v", err)
			if r.verbose {
				fmt.Printf("[ERROR] %s: %s\n", test.Name, errMsg)
			}
			metrics.AddRequestWithError(0, 0, false, errMsg)
			return
		}
	}

	// Create request
	var bodyReader io.Reader
	if bodyStr != "" {
		bodyReader = bytes.NewReader([]byte(bodyStr))
	}

	req, err := http.NewRequest(test.Method, test.Path, bodyReader)
	if err != nil {
		errMsg := fmt.Sprintf("failed to create request: %v", err)
		if r.verbose {
			fmt.Printf("[ERROR] %s: %s\n", test.Name, errMsg)
		}
		metrics.AddRequestWithError(0, 0, false, errMsg)
		return
	}

	// Evaluate and set headers
	for key, value := range test.Headers {
		evaluatedValue, err := r.evaluator.Evaluate(value)
		if err != nil {
			errMsg := fmt.Sprintf("header '%s' placeholder evaluation failed: %v", key, err)
			if r.verbose {
				fmt.Printf("[ERROR] %s: %s\n", test.Name, errMsg)
			}
			metrics.AddRequestWithError(0, 0, false, errMsg)
			return
		}
		req.Header.Set(key, evaluatedValue)
	}

	// Resolve and set auth headers (unless test opts out)
	if r.authResolver != nil && (test.Auth == nil || *test.Auth) {
		authHeaders, err := r.authResolver.ResolveHeaders(r.evaluator)
		if err != nil {
			// The detailed error may contain upstream response bodies or URLs
			// that could leak credentials; keep it out of the metrics (which
			// are served via /api/results). Only verbose stderr gets details.
			if r.verbose {
				fmt.Printf("[ERROR] %s: auth resolution failed: %v\n", test.Name, err)
			}
			metrics.AddRequestWithError(0, 0, false, "auth resolution failed")
			return
		}
		for k, v := range authHeaders {
			req.Header.Set(k, v)
		}
	}

	// Execute request
	startTime := time.Now()
	resp, err := r.client.Do(req)
	latency := time.Since(startTime)

	if err != nil {
		errMsg := fmt.Sprintf("HTTP request failed: %v", err)
		if r.verbose {
			fmt.Printf("[ERROR] %s: %s (latency: %v)\n", test.Name, errMsg, latency.Round(time.Millisecond))
		}
		metrics.AddRequestWithError(0, latency, false, errMsg)
		return
	}
	defer resp.Body.Close()

	// Read body for assertions; drain otherwise so the connection is reused
	var bodyBytes []byte
	if assertions.shouldReadBody(test.Assert) {
		bodyBytes, _ = io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // Limit to 1MB
	} else {
		io.Copy(io.Discard, resp.Body)
	}

	// Check assertions
	passed, assertionErr := assertions.checkAssertions(test.Assert, resp.StatusCode, bodyBytes, latency)
	if !passed && assertionErr != "" {
		if r.verbose {
			fmt.Printf("[ASSERTION FAILED] %s: %s (status: %d, latency: %v)\n",
				test.Name, assertionErr, resp.StatusCode, latency.Round(time.Millisecond))
		}
		metrics.AddRequestWithError(resp.StatusCode, latency, false, assertionErr)
	} else {
		if r.verbose && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			fmt.Printf("[OK] %s: status=%d latency=%v\n",
				test.Name, resp.StatusCode, latency.Round(time.Millisecond))
		}
		metrics.AddRequest(resp.StatusCode, latency, passed)
	}
}

// ProgressUpdate represents a progress update during test execution
type ProgressUpdate struct {
	TestName string
	Elapsed  time.Duration
	Total    int64
	Failures int64
	RPS      float64
	Done     bool
}
