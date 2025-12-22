package runner

import (
	"bytes"
	"fmt"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
)

type Assertions struct {
	evaluator *placeholders.Evaluator
}

func NewAssertions(evaluator *placeholders.Evaluator) *Assertions {
	return &Assertions{
		evaluator: evaluator,
	}
}

func (a *Assertions) shouldReadBody(assert *config.Assertion) bool {
	if assert == nil {
		return false
	}

	return assert.BodyContains != "" || assert.BodyEquals != "" || assert.BodyNotEquals != ""
}

// checkAssertions validates all assertions and returns (passed, errorMessage)
func (a *Assertions) checkAssertions(assert *config.Assertion, statusCode int, body []byte, latency time.Duration) (bool, string) {
	if assert == nil {
		return true, ""
	}

	// Check status code
	if assert.StatusCode != 0 && statusCode != assert.StatusCode {
		return false, fmt.Sprintf("expected status code %d, got %d", assert.StatusCode, statusCode)
	}

	// Check body contains
	if assert.BodyContains != "" {
		expected, err := a.evaluator.Evaluate(assert.BodyContains)
		if err != nil {
			return false, fmt.Sprintf("body_contains evaluation failed: %v", err)
		}

		if !bytes.Contains(body, []byte(expected)) {
			return false, fmt.Sprintf("body does not contain '%s'", expected)
		}
	}

	if assert.BodyEquals != "" {
		expected, err := a.evaluator.Evaluate(assert.BodyEquals)
		if err != nil {
			return false, fmt.Sprintf("body_equals evaluation failed: %v", err)
		}

		if string(body) != expected {
			return false, fmt.Sprintf("body does not equal '%s'", expected)
		}
	}

	if assert.BodyNotEquals != "" {
		expected, err := a.evaluator.Evaluate(assert.BodyNotEquals)
		if err != nil {
			return false, fmt.Sprintf("body_not_equals evaluation failed: %v", err)
		}

		if string(body) == expected {
			return false, fmt.Sprintf("body equals '%s'", expected)
		}
	}

	// Check max latency
	if assert.MaxLatencyMs > 0 {
		if latency > time.Duration(assert.MaxLatencyMs)*time.Millisecond {
			return false, fmt.Sprintf("latency %v exceeds max %dms", latency.Round(time.Millisecond), assert.MaxLatencyMs)
		}
	}

	return true, ""
}
