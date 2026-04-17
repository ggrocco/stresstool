package placeholders

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"
	"stresstool/internal/config"
)

// evalRequest is sent over the eval channel to the vm owner goroutine
type evalRequest struct {
	jsCode  string
	reply   chan evalReply
}

// evalReply carries the result of a JS evaluation back to the caller
type evalReply struct {
	result string
	err    error
}

// Evaluator handles placeholder evaluation
type Evaluator struct {
	config   *config.Config
	evalChan chan evalRequest
	stopChan chan struct{}
}

// NewEvaluator creates a new placeholder evaluator and starts the vm goroutine
func NewEvaluator(cfg *config.Config) *Evaluator {
	vm := goja.New()

	// Register built-in functions
	vm.Set("now", func() string {
		return time.Now().UTC().Format(time.RFC3339)
	})

	vm.Set("uuid", func() string {
		return uuid.New().String()
	})

	e := &Evaluator{
		config:   cfg,
		evalChan: make(chan evalRequest, 64),
		stopChan: make(chan struct{}),
	}

	// The vm goroutine is the sole owner of the goja runtime, eliminating the need for a mutex
	go e.runVM(vm)

	return e
}

// jsEvalTimeout caps how long a single {{ js(...) }} expression may run before
// goja is interrupted. Placeholder evaluations should complete in microseconds;
// anything longer is almost certainly a runaway script from a malicious or
// malformed config.
const jsEvalTimeout = 500 * time.Millisecond

// runVM is the dedicated goroutine that owns the goja runtime.
// All JS evaluations are serialised through this goroutine via evalChan.
func (e *Evaluator) runVM(vm *goja.Runtime) {
	for {
		select {
		case <-e.stopChan:
			return
		case req := <-e.evalChan:
			timer := time.AfterFunc(jsEvalTimeout, func() {
				vm.Interrupt("stresstool: JS evaluation exceeded timeout")
			})
			value, err := vm.RunString(req.jsCode)
			timer.Stop()
			vm.ClearInterrupt()
			if err != nil {
				req.reply <- evalReply{err: fmt.Errorf("JS evaluation error: %w", err)}
			} else {
				req.reply <- evalReply{result: value.String()}
			}
		}
	}
}

// Close shuts down the vm goroutine. Call this when the Evaluator is no longer needed.
func (e *Evaluator) Close() {
	close(e.stopChan)
}

var placeholderRegex = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// Evaluate replaces all placeholders in a string
func (e *Evaluator) Evaluate(s string) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}

	var firstErr error
	result := placeholderRegex.ReplaceAllStringFunc(s, func(match string) string {
		// Extract content between {{ }}
		content := strings.TrimSpace(match[2 : len(match)-2])

		value, err := e.evaluatePlaceholder(content)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("placeholder evaluation failed for '%s': %w", match, err)
			}
			return match // Return original on error
		}

		return value
	})

	if firstErr != nil {
		return "", firstErr
	}

	return result, nil
}

// evaluatePlaceholder evaluates a single placeholder expression
func (e *Evaluator) evaluatePlaceholder(expr string) (string, error) {
	expr = strings.TrimSpace(expr)

	// Handle js("...") syntax
	if strings.HasPrefix(expr, "js(") && strings.HasSuffix(expr, ")") {
		jsCode := strings.Trim(expr[3:len(expr)-1], `"'`)
		return e.evaluateJS(jsCode)
	}

	// Handle function calls like token(), now(), uuid()
	if strings.HasSuffix(expr, "()") {
		funcName := expr[:len(expr)-2]
		return e.evaluateFunction(funcName)
	}

	// Try as raw JS expression
	return e.evaluateJS(expr)
}

// evaluateFunction resolves a function call
func (e *Evaluator) evaluateFunction(name string) (string, error) {
	// Check if it's a custom function
	if funcDef := e.config.GetFunc(name); funcDef != nil {
		value, err := funcDef.ExecuteFunc()
		if err != nil {
			return "", err
		}
		return value, nil
	}

	// Delegate built-in JS function calls to the vm goroutine via the eval channel
	return e.evaluateJS(name + "()")
}

// evaluateJS sends a JS expression to the vm goroutine and returns the result
func (e *Evaluator) evaluateJS(jsCode string) (string, error) {
	reply := make(chan evalReply, 1)
	e.evalChan <- evalRequest{jsCode: jsCode, reply: reply}
	r := <-reply
	return r.result, r.err
}
