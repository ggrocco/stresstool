package placeholders

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"
	"stresstool/internal/config"
)

// Evaluator handles placeholder evaluation
type Evaluator struct {
	config   *config.Config
	vm       *goja.Runtime
	vmMutex  sync.Mutex
}

// NewEvaluator creates a new placeholder evaluator
func NewEvaluator(cfg *config.Config) *Evaluator {
	vm := goja.New()
	
	// Register built-in functions
	vm.Set("now", func() string {
		return time.Now().UTC().Format(time.RFC3339)
	})
	
	vm.Set("uuid", func() string {
		return uuid.New().String()
	})

	return &Evaluator{
		config: cfg,
		vm:     vm,
	}
}

var placeholderRegex = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// Evaluate replaces all placeholders in a string
func (e *Evaluator) Evaluate(s string) (string, error) {
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
	
	// Check if it's a built-in JS function
	e.vmMutex.Lock()
	defer e.vmMutex.Unlock()
	
	if fn, ok := goja.AssertFunction(e.vm.Get(name)); ok {
		result, err := fn(goja.Undefined())
		if err != nil {
			return "", fmt.Errorf("failed to execute built-in function %s: %w", name, err)
		}
		return result.String(), nil
	}
	
	return "", fmt.Errorf("unknown function: %s", name)
}

// evaluateJS evaluates a JavaScript expression
func (e *Evaluator) evaluateJS(jsCode string) (string, error) {
	e.vmMutex.Lock()
	defer e.vmMutex.Unlock()
	
	value, err := e.vm.RunString(jsCode)
	if err != nil {
		return "", fmt.Errorf("JS evaluation error: %w", err)
	}
	
	return value.String(), nil
}

