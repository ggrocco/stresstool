package cli

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"stresstool/internal/config"
	"stresstool/internal/placeholders"
	"stresstool/internal/runner"
)

// Run executes the stress test tool
func Run(configFile string, verbose bool, dryRun bool, parallel bool) error {
	// Load configuration
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	// Validate placeholders in dry-run mode
	if dryRun {
		return dryRunValidation(cfg)
	}

	// Create evaluator
	eval := placeholders.NewEvaluator(cfg)

	// Create runner
	r := runner.NewRunner(eval, verbose)

	// Run tests
	results := make([]*runner.TestResult, len(cfg.Tests))
	progressChan := make(chan runner.ProgressUpdate, 100)

	// Start progress display
	go displayProgress(progressChan)

	if parallel {
		var wg sync.WaitGroup

		for i := range cfg.Tests {
			wg.Add(1)
			test := cfg.Tests[i]
			index := i
			go func(t config.Test, resultIndex int) {
				defer wg.Done()
				results[resultIndex] = r.RunTest(&t, progressChan)
			}(test, index)
		}

		wg.Wait()
	} else {
		for i := range cfg.Tests {
			test := cfg.Tests[i]
			results[i] = r.RunTest(&test, progressChan)
		}
	}

	close(progressChan)
	time.Sleep(100 * time.Millisecond) // Allow final progress update

	// Print summary
	printSummary(results)

	// Check if any test failed
	for _, result := range results {
		if !result.Passed {
			return fmt.Errorf("one or more tests failed")
		}
	}

	return nil
}

// dryRunValidation validates configuration without running tests
func dryRunValidation(cfg *config.Config) error {
	fmt.Println("=== DRY RUN MODE ===")
	fmt.Println()

	// Validate funcs
	fmt.Printf("Functions defined: %d\n", len(cfg.Funcs))
	for _, f := range cfg.Funcs {
		fmt.Printf("  - %s: %s\n", f.Name, strings.Join(f.Cmd, " "))
	}
	fmt.Println()

	// Validate tests
	fmt.Printf("Tests defined: %d\n", len(cfg.Tests))
	for i, test := range cfg.Tests {
		fmt.Printf("\nTest %d: %s\n", i+1, test.Name)
		fmt.Printf("  Path: %s\n", test.Path)
		fmt.Printf("  Method: %s\n", test.Method)
		fmt.Printf("  RPS: %d\n", test.RequestsPerSecond)
		fmt.Printf("  Threads: %d\n", test.Threads)
		fmt.Printf("  Duration: %ds\n", test.RunSeconds)

		if test.Assert != nil {
			fmt.Printf("  Assertions:\n")
			if test.Assert.StatusCode != 0 {
				fmt.Printf("    - Status Code: %d\n", test.Assert.StatusCode)
			}
			if test.Assert.BodyContains != "" {
				fmt.Printf("    - Body Contains: %s\n", test.Assert.BodyContains)
			}
			if test.Assert.MaxLatencyMs > 0 {
				fmt.Printf("    - Max Latency: %dms\n", test.Assert.MaxLatencyMs)
			}
		}
	}

	// Validate placeholders
	eval := placeholders.NewEvaluator(cfg)
	fmt.Println("\n=== Placeholder Validation ===")

	for _, test := range cfg.Tests {
		// Check headers
		for key, value := range test.Headers {
			if strings.Contains(value, "{{") {
				_, err := eval.Evaluate(value)
				if err != nil {
					return fmt.Errorf("invalid placeholder in header %s: %w", key, err)
				}
			}
		}

		// Check body
		if strings.Contains(test.Body, "{{") {
			_, err := eval.Evaluate(test.Body)
			if err != nil {
				return fmt.Errorf("invalid placeholder in body: %w", err)
			}
		}
	}

	fmt.Println("✓ All placeholders are valid")
	fmt.Println("\n=== DRY RUN COMPLETE ===")

	return nil
}

// displayProgress shows real-time progress updates
func displayProgress(progressChan <-chan runner.ProgressUpdate) {
	updates := make(map[string]runner.ProgressUpdate)
	lineCount := 0

	for update := range progressChan {
		updates[update.TestName] = update

		// Move cursor up to overwrite previous lines
		if lineCount > 0 {
			fmt.Printf("\033[%dA", lineCount) // Move up
		}

		// Print all active tests
		lineCount = 0
		for _, u := range updates {
			if u.Done {
				fmt.Printf("✓ %s: COMPLETE - %d requests, %.1f RPS, %d failures\n",
					u.TestName, u.Total, u.RPS, u.Failures)
			} else {
				elapsedSec := int(u.Elapsed.Seconds())
				fmt.Printf("→ %s: %ds elapsed - %d requests, %.1f RPS, %d failures\n",
					u.TestName, elapsedSec, u.Total, u.RPS, u.Failures)
			}
			lineCount++
		}
	}

	// Move cursor down after final update
	if lineCount > 0 {
		fmt.Printf("\033[%dB", lineCount)
	}
}

// printSummary prints the final test results
func printSummary(results []*runner.TestResult) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("TEST SUMMARY")
	fmt.Println(strings.Repeat("=", 80))

	allPassed := true

	for _, result := range results {
		test := result.Test
		metrics := result.Metrics

		fmt.Printf("\nTest: %s\n", test.Name)
		fmt.Printf("  Path: %s %s\n", test.Method, test.Path)
		fmt.Printf("  Duration: %ds\n", test.RunSeconds)
		fmt.Printf("  Requests: %d total, %d success, %d failures\n",
			metrics.TotalRequests, metrics.SuccessCount, metrics.FailureCount)

		// Error breakdown
		metrics.ErrorsMutex.Lock()
		if len(metrics.Errors) > 0 {
			fmt.Printf("  Errors:\n")
			// Sort errors by count (descending)
			type errorCount struct {
				err   string
				count int64
			}
			errorList := make([]errorCount, 0, len(metrics.Errors))
			for err, count := range metrics.Errors {
				errorList = append(errorList, errorCount{err: err, count: count})
			}
			sort.Slice(errorList, func(i, j int) bool {
				return errorList[i].count > errorList[j].count
			})

			// Show top 10 errors
			maxErrors := 10
			if len(errorList) < maxErrors {
				maxErrors = len(errorList)
			}
			for i := 0; i < maxErrors; i++ {
				fmt.Printf("    - %s (count: %d)\n", errorList[i].err, errorList[i].count)
			}
			if len(errorList) > maxErrors {
				fmt.Printf("    ... and %d more error types\n", len(errorList)-maxErrors)
			}
		}
		metrics.ErrorsMutex.Unlock()

		// Latency metrics
		min, max, avg := metrics.GetMinMaxAvg()
		p95 := metrics.GetPercentile(0.95)
		p99 := metrics.GetPercentile(0.99)

		fmt.Printf("  Latency:\n")
		fmt.Printf("    Min: %s\n", min.Round(time.Millisecond))
		fmt.Printf("    Avg: %s\n", avg.Round(time.Millisecond))
		fmt.Printf("    Max: %s\n", max.Round(time.Millisecond))
		fmt.Printf("    P95: %s\n", p95.Round(time.Millisecond))
		fmt.Printf("    P99: %s\n", p99.Round(time.Millisecond))

		// Assertions
		if test.Assert != nil {
			fmt.Printf("  Assertions:\n")
			if test.Assert.StatusCode != 0 {
				fmt.Printf("    Status Code %d: ", test.Assert.StatusCode)
				if metrics.StatusCodes[test.Assert.StatusCode] == metrics.TotalRequests {
					fmt.Println("✓ PASSED")
				} else {
					fmt.Printf("✗ FAILED (%d/%d passed)\n",
						metrics.StatusCodes[test.Assert.StatusCode], metrics.TotalRequests)
				}
			}
			if test.Assert.BodyContains != "" {
				fmt.Printf("    Body Contains '%s': ", test.Assert.BodyContains)
				if metrics.AssertionFailures == 0 {
					fmt.Println("✓ PASSED")
				} else {
					fmt.Printf("✗ FAILED (%d failures)\n", metrics.AssertionFailures)
				}
			}
			if test.Assert.MaxLatencyMs > 0 {
				fmt.Printf("    Max Latency %dms: ", test.Assert.MaxLatencyMs)
				violations := int64(0)
				metrics.LatenciesMutex.Lock()
				for _, lat := range metrics.Latencies {
					if lat > time.Duration(test.Assert.MaxLatencyMs)*time.Millisecond {
						violations++
					}
				}
				metrics.LatenciesMutex.Unlock()
				if violations == 0 {
					fmt.Println("✓ PASSED")
				} else {
					fmt.Printf("✗ FAILED (%d violations)\n", violations)
				}
			}
		}

		// Overall result
		if result.Passed {
			fmt.Printf("  Result: ✓ PASSED\n")
		} else {
			fmt.Printf("  Result: ✗ FAILED\n")
			allPassed = false
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	if allPassed {
		fmt.Println("OVERALL RESULT: ✓ ALL TESTS PASSED")
	} else {
		fmt.Println("OVERALL RESULT: ✗ SOME TESTS FAILED")
	}
	fmt.Println(strings.Repeat("=", 80))
}
