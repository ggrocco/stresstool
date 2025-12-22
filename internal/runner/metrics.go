package runner

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics holds statistics for a test run
type Metrics struct {
	TotalRequests     int64
	SuccessCount      int64
	FailureCount      int64
	AssertionFailures int64
	Latencies         []time.Duration
	LatenciesMutex    sync.Mutex
	StatusCodes       map[int]int64
	StatusCodesMutex  sync.Mutex
	Errors            map[string]int64
	ErrorsMutex       sync.Mutex
}

// NewMetrics creates a new metrics collector
func NewMetrics() *Metrics {
	return &Metrics{
		Latencies:   make([]time.Duration, 0),
		StatusCodes: make(map[int]int64),
		Errors:      make(map[string]int64),
	}
}

// AddRequest records a request result
func (m *Metrics) AddRequest(statusCode int, latency time.Duration, assertionPassed bool) {
	m.AddRequestWithError(statusCode, latency, assertionPassed, "")
}

// AddRequestWithError records a request result with an optional error message
func (m *Metrics) AddRequestWithError(statusCode int, latency time.Duration, assertionPassed bool, errMsg string) {
	atomic.AddInt64(&m.TotalRequests, 1)

	m.StatusCodesMutex.Lock()
	m.StatusCodes[statusCode]++
	m.StatusCodesMutex.Unlock()

	m.LatenciesMutex.Lock()
	m.Latencies = append(m.Latencies, latency)
	m.LatenciesMutex.Unlock()

	// Track error if present
	if errMsg != "" {
		m.ErrorsMutex.Lock()
		m.Errors[errMsg]++
		m.ErrorsMutex.Unlock()
	}

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
