package runner

import (
	"sync"
	"testing"
	"time"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics()
	defer m.Stop()

	if m.TotalRequests != 0 {
		t.Errorf("expected TotalRequests=0, got %d", m.TotalRequests)
	}
	if len(m.Latencies) != 0 {
		t.Errorf("expected empty Latencies, got %d entries", len(m.Latencies))
	}
	if len(m.StatusCodes) != 0 {
		t.Errorf("expected empty StatusCodes, got %d entries", len(m.StatusCodes))
	}
	if len(m.Errors) != 0 {
		t.Errorf("expected empty Errors, got %d entries", len(m.Errors))
	}
}

func TestAddRequest_SuccessCount(t *testing.T) {
	m := NewMetrics()

	m.AddRequest(200, 10*time.Millisecond, true)
	m.AddRequest(200, 20*time.Millisecond, true)
	m.AddRequest(500, 5*time.Millisecond, false)
	m.Stop()

	if m.TotalRequests != 3 {
		t.Errorf("expected TotalRequests=3, got %d", m.TotalRequests)
	}
	if m.SuccessCount != 2 {
		t.Errorf("expected SuccessCount=2, got %d", m.SuccessCount)
	}
	if m.FailureCount != 1 {
		t.Errorf("expected FailureCount=1, got %d", m.FailureCount)
	}
}

func TestAddRequest_AssertionFailure(t *testing.T) {
	m := NewMetrics()

	m.AddRequest(200, 10*time.Millisecond, false) // 2xx but assertion failed
	m.Stop()

	if m.SuccessCount != 0 {
		t.Errorf("expected SuccessCount=0, got %d", m.SuccessCount)
	}
	if m.FailureCount != 1 {
		t.Errorf("expected FailureCount=1, got %d", m.FailureCount)
	}
	if m.AssertionFailures != 1 {
		t.Errorf("expected AssertionFailures=1, got %d", m.AssertionFailures)
	}
}

func TestAddRequestWithError_RecordsError(t *testing.T) {
	m := NewMetrics()

	m.AddRequestWithError(0, 0, false, "connection refused")
	m.AddRequestWithError(0, 0, false, "connection refused")
	m.AddRequestWithError(0, 0, false, "timeout")
	m.Stop()

	if m.Errors["connection refused"] != 2 {
		t.Errorf("expected 'connection refused'=2, got %d", m.Errors["connection refused"])
	}
	if m.Errors["timeout"] != 1 {
		t.Errorf("expected 'timeout'=1, got %d", m.Errors["timeout"])
	}
}

func TestAddRequest_StatusCodeTracking(t *testing.T) {
	m := NewMetrics()

	m.AddRequest(200, 1*time.Millisecond, true)
	m.AddRequest(200, 1*time.Millisecond, true)
	m.AddRequest(404, 1*time.Millisecond, false)
	m.AddRequest(500, 1*time.Millisecond, false)
	m.Stop()

	if m.StatusCodes[200] != 2 {
		t.Errorf("expected StatusCodes[200]=2, got %d", m.StatusCodes[200])
	}
	if m.StatusCodes[404] != 1 {
		t.Errorf("expected StatusCodes[404]=1, got %d", m.StatusCodes[404])
	}
	if m.StatusCodes[500] != 1 {
		t.Errorf("expected StatusCodes[500]=1, got %d", m.StatusCodes[500])
	}
}

func TestGetPercentile(t *testing.T) {
	m := NewMetrics()

	for i := 1; i <= 100; i++ {
		m.AddRequest(200, time.Duration(i)*time.Millisecond, true)
	}
	m.Stop()

	p50 := m.GetPercentile(0.50)
	if p50 < 49*time.Millisecond || p50 > 51*time.Millisecond {
		t.Errorf("unexpected p50: %v", p50)
	}

	p95 := m.GetPercentile(0.95)
	if p95 < 94*time.Millisecond || p95 > 96*time.Millisecond {
		t.Errorf("unexpected p95: %v", p95)
	}

	p99 := m.GetPercentile(0.99)
	if p99 < 98*time.Millisecond || p99 > 100*time.Millisecond {
		t.Errorf("unexpected p99: %v", p99)
	}
}

func TestGetPercentile_Empty(t *testing.T) {
	m := NewMetrics()
	m.Stop()

	if got := m.GetPercentile(0.99); got != 0 {
		t.Errorf("expected 0 for empty latencies, got %v", got)
	}
}

func TestGetMinMaxAvg(t *testing.T) {
	m := NewMetrics()

	m.AddRequest(200, 10*time.Millisecond, true)
	m.AddRequest(200, 30*time.Millisecond, true)
	m.AddRequest(200, 20*time.Millisecond, true)
	m.Stop()

	min, max, avg := m.GetMinMaxAvg()
	if min != 10*time.Millisecond {
		t.Errorf("expected min=10ms, got %v", min)
	}
	if max != 30*time.Millisecond {
		t.Errorf("expected max=30ms, got %v", max)
	}
	if avg != 20*time.Millisecond {
		t.Errorf("expected avg=20ms, got %v", avg)
	}
}

func TestGetMinMaxAvg_Empty(t *testing.T) {
	m := NewMetrics()
	m.Stop()

	min, max, avg := m.GetMinMaxAvg()
	if min != 0 || max != 0 || avg != 0 {
		t.Errorf("expected all zeros for empty latencies, got min=%v max=%v avg=%v", min, max, avg)
	}
}

// TestConcurrentAdd verifies the channel-based aggregator handles concurrent
// writes from many goroutines without data races.
func TestConcurrentAdd(t *testing.T) {
	m := NewMetrics()

	const goroutines = 20
	const requestsEach = 50

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsEach; j++ {
				m.AddRequest(200, time.Millisecond, true)
			}
		}()
	}
	wg.Wait()
	m.Stop()

	total := int64(goroutines * requestsEach)
	if m.TotalRequests != total {
		t.Errorf("expected TotalRequests=%d, got %d", total, m.TotalRequests)
	}
	if m.SuccessCount != total {
		t.Errorf("expected SuccessCount=%d, got %d", total, m.SuccessCount)
	}
	if int64(len(m.Latencies)) != total {
		t.Errorf("expected %d latency entries, got %d", total, len(m.Latencies))
	}
}
