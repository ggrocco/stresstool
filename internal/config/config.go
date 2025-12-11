package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level configuration structure
type Config struct {
	Funcs []FuncDef `yaml:"funcs"`
	Tests []Test    `yaml:"tests"`
}

// FuncDef defines a custom function that can be called via placeholders
type FuncDef struct {
	Name string   `yaml:"name"`
	Cmd  []string `yaml:"cmd"`
}

// Test defines a single HTTP stress test
type Test struct {
	Name              string            `yaml:"name"`
	Path              string            `yaml:"path"`
	Method            string            `yaml:"method"`
	RequestsPerSecond int               `yaml:"requests_per_second"`
	Threads           int               `yaml:"threads"`
	RunSeconds        int               `yaml:"run_seconds"`
	Headers           map[string]string `yaml:"headers"`
	Body              string            `yaml:"body"`
	Assert            *Assertion        `yaml:"assert"`
}

// Assertion defines what to check in responses
type Assertion struct {
	StatusCode   int    `yaml:"status_code"`
	BodyContains string `yaml:"body_contains"`
	MaxLatencyMs int    `yaml:"max_latency_ms"`
}

// LoadConfig reads and parses a YAML configuration file
func LoadConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &config, nil
}

// Validate checks that the configuration is valid
func (c *Config) Validate() error {
	// Check for duplicate func names
	funcNames := make(map[string]bool)
	for _, f := range c.Funcs {
		if f.Name == "" {
			return fmt.Errorf("func name cannot be empty")
		}
		if funcNames[f.Name] {
			return fmt.Errorf("duplicate func name: %s", f.Name)
		}
		if len(f.Cmd) == 0 {
			return fmt.Errorf("func %s: cmd cannot be empty", f.Name)
		}
		funcNames[f.Name] = true
	}

	// Validate each test
	for i, test := range c.Tests {
		if test.Path == "" {
			return fmt.Errorf("test[%d]: path is required", i)
		}
		if test.RequestsPerSecond <= 0 {
			return fmt.Errorf("test[%d]: requests_per_second must be > 0", i)
		}
		if test.Threads <= 0 {
			return fmt.Errorf("test[%d]: threads must be > 0", i)
		}
		if test.RunSeconds <= 0 {
			return fmt.Errorf("test[%d]: run_seconds must be > 0", i)
		}
		if test.Method == "" {
			c.Tests[i].Method = "GET"
		}
	}

	return nil
}

// GetFunc returns a function definition by name
func (c *Config) GetFunc(name string) *FuncDef {
	for _, f := range c.Funcs {
		if f.Name == name {
			return &f
		}
	}
	return nil
}

// ExecuteFunc runs a custom function command and returns its stdout
func (f *FuncDef) ExecuteFunc() (string, error) {
	if len(f.Cmd) == 0 {
		return "", fmt.Errorf("empty command")
	}

	cmd := exec.Command(f.Cmd[0], f.Cmd[1:]...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to execute %s: %w", f.Name, err)
	}

	return strings.TrimSpace(string(output)), nil
}

