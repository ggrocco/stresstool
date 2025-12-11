package runner

import (
	"bytes"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

// Metrics holds statistics for a test run
type Metrics struct {
	TotalRequests    int64
	SuccessCount     int64
	FailureCount     int64
	AssertionFailures int64
	Latencies        []time.Duration
	LatenciesMutex   sync.Mutex
	StatusCodes      map[int]int64
	StatusCodesMutex sync.Mutex
}

// NewMetrics creates a new metrics collector
func NewMetrics() *Metrics {
	return &Metrics{
		Latencies:   make([]time.Duration, 0),
		StatusCodes: make(map[int]int64),
	}
}

// AddRequest records a request result
func (m *Metrics) AddRequest(statusCode int, latency time.Duration, assertionPassed bool) {
	atomic.AddInt64(&m.TotalRequests, 1)
	
	m.StatusCodesMutex.Lock()
	m.StatusCodes[statusCode]++
	m.StatusCodesMutex.Unlock()
	
	m.LatenciesMutex.Lock()
	m.Latencies = append(m.Latencies, latency)
	m.LatenciesMutex.Unlock()
	
	if statusCode >= 200 && statusCode < 300 {
		if assertionPassed {
			atomic.AddInt64(&m.SuccessCount, 1)
		} else {
			atomic.AddInt64(&m.FailureCount, 1)
			atomic.AddInt64(&m.AssertionFailures, 1)
		}
	} else {
		atomic.AddInt64(&m.FailureCount, 1)
	}
}

// GetPercentile calculates a percentile from latencies
func (m *Metrics) GetPercentile(p float64) time.Duration {
	m.LatenciesMutex.Lock()
	defer m.LatenciesMutex.Unlock()
	
	if len(m.Latencies) == 0 {
		return 0
	}
	
	sorted := make([]time.Duration, len(m.Latencies))
	copy(sorted, m.Latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	
	index := int(float64(len(sorted)) * p)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	
	return sorted[index]
}

// GetMinMaxAvg calculates min, max, and average latencies
func (m *Metrics) GetMinMaxAvg() (min, max, avg time.Duration) {
	m.LatenciesMutex.Lock()
	defer m.LatenciesMutex.Unlock()
	
	if len(m.Latencies) == 0 {
		return 0, 0, 0
	}
	
	min = m.Latencies[0]
	max = m.Latencies[0]
	var sum time.Duration
	
	for _, lat := range m.Latencies {
		if lat < min {
			min = lat
		}
		if lat > max {
			max = lat
		}
		sum += lat
	}
	
	avg = sum / time.Duration(len(m.Latencies))
	return
}

// TestResult holds the result of running a test
type TestResult struct {
	Test    *config.Test
	Metrics *Metrics
	Passed  bool
	Errors  []string
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
	result := &TestResult{
		Test:    test,
		Metrics: metrics,
		Passed:  true,
		Errors:  make([]string, 0),
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
					TestName:    test.Name,
					Elapsed:     elapsed,
					Total:       total,
					Failures:    failures,
					RPS:         float64(total) / elapsed.Seconds(),
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
					r.executeRequest(test, metrics)
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
		TestName:    test.Name,
		Elapsed:     elapsed,
		Total:       total,
		Failures:    failures,
		RPS:         float64(total) / elapsed.Seconds(),
		Done:        true,
	}
	
	// Check if test passed
	if metrics.AssertionFailures > 0 || metrics.FailureCount > 0 {
		result.Passed = false
	}
	
	return result
}

// executeRequest performs a single HTTP request
func (r *Runner) executeRequest(test *config.Test, metrics *Metrics) {
	// Evaluate body
	bodyStr := test.Body
	if bodyStr != "" {
		var err error
		bodyStr, err = r.evaluator.Evaluate(bodyStr)
		if err != nil {
			metrics.AddRequest(0, 0, false)
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
		metrics.AddRequest(0, 0, false)
		return
	}
	
	// Evaluate and set headers
	for key, value := range test.Headers {
		evaluatedValue, err := r.evaluator.Evaluate(value)
		if err != nil {
			metrics.AddRequest(0, 0, false)
			return
		}
		req.Header.Set(key, evaluatedValue)
	}
	
	// Execute request
	startTime := time.Now()
	resp, err := r.client.Do(req)
	latency := time.Since(startTime)
	
	if err != nil {
		metrics.AddRequest(0, latency, false)
		return
	}
	defer resp.Body.Close()
	
	// Read body for assertions
	var bodyBytes []byte
	if test.Assert != nil && test.Assert.BodyContains != "" {
		bodyBytes, _ = io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // Limit to 1MB
	}
	
	// Check assertions
	passed := r.checkAssertions(test.Assert, resp.StatusCode, bodyBytes, latency)
	metrics.AddRequest(resp.StatusCode, latency, passed)
}

// checkAssertions validates all assertions
func (r *Runner) checkAssertions(assert *config.Assertion, statusCode int, body []byte, latency time.Duration) bool {
	if assert == nil {
		return true
	}
	
	// Check status code
	if assert.StatusCode != 0 && statusCode != assert.StatusCode {
		return false
	}
	
	// Check body contains
	if assert.BodyContains != "" {
		if !bytes.Contains(body, []byte(assert.BodyContains)) {
			return false
		}
	}
	
	// Check max latency
	if assert.MaxLatencyMs > 0 {
		if latency > time.Duration(assert.MaxLatencyMs)*time.Millisecond {
			return false
		}
	}
	
	return true
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

