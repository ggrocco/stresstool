package placeholders

import (
	"strings"
	"sync"
	"testing"
	"time"

	"stresstool/internal/config"
)

func newTestEvaluator() *Evaluator {
	return NewEvaluator(&config.Config{})
}

func TestEvaluate_NoPlaceholders(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	got, err := e.Evaluate("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestEvaluate_UUID(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	got, err := e.Evaluate("{{uuid()}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	parts := strings.Split(got, "-")
	if len(parts) != 5 {
		t.Errorf("expected UUID format, got %q", got)
	}
}

func TestEvaluate_Now(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	got, err := e.Evaluate("{{now()}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// RFC3339 timestamps contain a 'T'
	if !strings.Contains(got, "T") {
		t.Errorf("expected RFC3339 timestamp, got %q", got)
	}
}

func TestEvaluate_JSExpression(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	got, err := e.Evaluate(`{{js("1 + 2")}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3" {
		t.Errorf("expected '3', got %q", got)
	}
}

func TestEvaluate_RawJSExpression(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	got, err := e.Evaluate(`{{"hello " + "world"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestEvaluate_MultipleInSameString(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	got, err := e.Evaluate(`prefix-{{uuid()}}-suffix`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "prefix-") {
		t.Errorf("expected 'prefix-' prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "-suffix") {
		t.Errorf("expected '-suffix' suffix, got %q", got)
	}
}

func TestEvaluate_UnknownFunction(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	_, err := e.Evaluate("{{doesNotExist()}}")
	if err == nil {
		t.Fatal("expected error for unknown function, got nil")
	}
}

func TestEvaluate_InvalidJS(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	_, err := e.Evaluate(`{{js("(((invalid")}}`)
	if err == nil {
		t.Fatal("expected error for invalid JS, got nil")
	}
}

// TestEvaluate_InfiniteLoopTimesOut verifies that a runaway JS expression is
// interrupted so the evaluator goroutine isn't permanently hung.
func TestEvaluate_InfiniteLoopTimesOut(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	done := make(chan error, 1)
	go func() {
		// No braces inside the JS string — the placeholder regex stops at `}`,
		// so the whole body must be brace-free to actually reach the VM.
		_, err := e.Evaluate(`{{js("while(true);")}}`)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("evaluation did not time out")
	}

	// Evaluator must still work after the interrupt.
	got, err := e.Evaluate(`{{js("1 + 1")}}`)
	if err != nil {
		t.Fatalf("evaluator broken after timeout: %v", err)
	}
	if got != "2" {
		t.Fatalf("expected 2, got %q", got)
	}
}

// TestConcurrentEvaluate verifies the channel-based vm goroutine handles
// concurrent calls without data races.
func TestConcurrentEvaluate(t *testing.T) {
	e := newTestEvaluator()
	defer e.Close()

	const goroutines = 20
	errs := make(chan error, goroutines)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.Evaluate("{{uuid()}}")
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent evaluation error: %v", err)
	}
}
