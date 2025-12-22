package runner

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

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
}

// Runner executes stress tests
type Runner struct {
	evaluator *placeholders.Evaluator
	client    *http.Client
	verbose   bool
}

// NewRunner creates a new test runner
func NewRunner(evaluator *placeholders.Evaluator, verbose bool) *Runner {
	return &Runner{
		evaluator: evaluator,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		verbose: verbose,
	}
}

// RunTest executes a single test
func (r *Runner) RunTest(test *config.Test, progressChan chan<- ProgressUpdate) *TestResult {
	metrics := NewMetrics()
	assertions := NewAssertions(r.evaluator)
	result := &TestResult{
		Test:       test,
		Metrics:    metrics,
		Assertions: assertions,
		Passed:     true,
		Errors:     make([]string, 0),
	}

	// Create request channel for rate limiting
	requestChan := make(chan struct{}, test.Threads*2)

	// Create worker pool
	var wg sync.WaitGroup
	stopChan := make(chan struct{})
	startTime := time.Now()
	endTime := startTime.Add(time.Duration(test.RunSeconds) * time.Second)

	// Rate limiter: send tokens to request channel at desired RPS
	interval := time.Duration(float64(time.Second) / float64(test.RequestsPerSecond))
	rateTicker := time.NewTicker(interval)
	defer rateTicker.Stop()

	go func() {
		defer close(requestChan)
		for {
			select {
			case <-stopChan:
				return
			case <-rateTicker.C:
				if time.Now().After(endTime) {
					return
				}
				select {
				case requestChan <- struct{}{}:
				default:
					// Channel full, skip (shouldn't happen with proper sizing)
				}
			}
		}
	}()

	// Progress reporting goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopChan:
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
				select {
				case <-stopChan:
					return
				case _, ok := <-requestChan:
					if !ok {
						return
					}
					if time.Now().After(endTime) {
						return
					}
					r.executeRequest(test, metrics, assertions)
				}
			}
		}()
	}

	// Wait for duration
	time.Sleep(time.Duration(test.RunSeconds) * time.Second)
	close(stopChan)
	wg.Wait()

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

	// Read body for assertions
	var bodyBytes []byte
	if assertions.shouldReadBody(test.Assert) {
		bodyBytes, _ = io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // Limit to 1MB
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
