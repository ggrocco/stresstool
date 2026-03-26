package runner

import (
	"sort"
	"sync/atomic"
	"time"
)

// metricEvent represents a single request result sent to the aggregator
type metricEvent struct {
	statusCode      int
	latency         time.Duration
	assertionPassed bool
	errMsg          string
}

// Metrics holds statistics for a test run
type Metrics struct {
	TotalRequests     int64
	SuccessCount      int64
	FailureCount      int64
	AssertionFailures int64
	Latencies         []time.Duration
	StatusCodes       map[int]int64
	Errors            map[string]int64

	metricsChan   chan metricEvent
	aggregateDone chan struct{}
}

// NewMetrics creates a new metrics collector and starts the aggregator goroutine
func NewMetrics() *Metrics {
	m := &Metrics{
		Latencies:     make([]time.Duration, 0),
		StatusCodes:   make(map[int]int64),
		Errors:        make(map[string]int64),
		metricsChan:   make(chan metricEvent, 1000),
		aggregateDone: make(chan struct{}),
	}
	go m.aggregate()
	return m
}

// aggregate is the single goroutine that owns and updates Latencies, StatusCodes, and Errors
func (m *Metrics) aggregate() {
	for event := range m.metricsChan {
		m.StatusCodes[event.statusCode]++
		m.Latencies = append(m.Latencies, event.latency)
		if event.errMsg != "" {
			m.Errors[event.errMsg]++
		}
	}
	close(m.aggregateDone)
}

// Stop closes the metrics channel and waits for the aggregator to drain all events
func (m *Metrics) Stop() {
	close(m.metricsChan)
	<-m.aggregateDone
}

// AddRequest records a request result
func (m *Metrics) AddRequest(statusCode int, latency time.Duration, assertionPassed bool) {
	m.AddRequestWithError(statusCode, latency, assertionPassed, "")
}

// AddRequestWithError records a request result with an optional error message
func (m *Metrics) AddRequestWithError(statusCode int, latency time.Duration, assertionPassed bool, errMsg string) {
	atomic.AddInt64(&m.TotalRequests, 1)

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

	m.metricsChan <- metricEvent{
		statusCode:      statusCode,
		latency:         latency,
		assertionPassed: assertionPassed,
		errMsg:          errMsg,
	}
}

// GetPercentile calculates a percentile from latencies.
// Must only be called after Stop() to ensure all events have been aggregated.
func (m *Metrics) GetPercentile(p float64) time.Duration {
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

// GetMinMaxAvg calculates min, max, and average latencies.
// Must only be called after Stop() to ensure all events have been aggregated.
func (m *Metrics) GetMinMaxAvg() (min, max, avg time.Duration) {
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
