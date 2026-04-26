package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"stresstool/internal/auth"
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
	defer eval.Close()

	// Create auth resolver
	var authResolver *auth.Resolver
	if cfg.Auth != nil {
		authResolver = auth.NewResolver(cfg.Auth)
		defer authResolver.Close()
	}

	// Create runner
	r := runner.NewRunner(eval, verbose, authResolver)

	// Run tests
	results := make([]*runner.TestResult, len(cfg.Tests))
	progressChan := make(chan runner.ProgressUpdate, 100)

	for i, test := range cfg.Tests {
		if test.WarmupSeconds > 0 {
			fmt.Printf("Test %d (%s): %d RPS, %d threads, %ds duration (+%ds warmup)\n",
				i+1, test.Name, test.RequestsPerSecond, test.Threads, test.RunSeconds, test.WarmupSeconds)
		} else {
			fmt.Printf("Test %d (%s): %d RPS, %d threads, %ds duration\n",
				i+1, test.Name, test.RequestsPerSecond, test.Threads, test.RunSeconds)
		}
	}
	fmt.Println()

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
				results[resultIndex] = r.RunTest(context.Background(), &t, progressChan)
			}(test, index)
		}

		wg.Wait()
	} else {
		for i := range cfg.Tests {
			test := cfg.Tests[i]
			results[i] = r.RunTest(context.Background(), &test, progressChan)
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

	// Display auth config
	if cfg.Auth != nil {
		authType := cfg.Auth.AuthType()
		if authType != "" {
			fmt.Printf("Auth: %s\n", authType)
			switch {
			case cfg.Auth.JWT != nil:
				alg := "HS256"
				if a := cfg.Auth.JWT.Header["alg"]; a != "" {
					alg = a
				}
				fmt.Printf("  Alg: %s\n", alg)
				fmt.Printf("  Header fields: %d (overrides merged on top of defaults)\n", len(cfg.Auth.JWT.Header))
				fmt.Printf("  Payload fields: %d (overrides merged on top of defaults)\n", len(cfg.Auth.JWT.Payload))
			case cfg.Auth.BasicAuth != nil:
				fmt.Printf("  Username: %s\n", cfg.Auth.BasicAuth.Username)
			case cfg.Auth.Bearer != nil:
				fmt.Printf("  Token: %s\n", cfg.Auth.Bearer.Token)
			case cfg.Auth.APIKey != nil:
				fmt.Printf("  Header: %s\n", cfg.Auth.APIKey.Header)
			case cfg.Auth.OAuth2ClientCredentials != nil:
				fmt.Printf("  Token URL: %s\n", cfg.Auth.OAuth2ClientCredentials.TokenURL)
				fmt.Printf("  Client ID: %s\n", cfg.Auth.OAuth2ClientCredentials.ClientID)
			}
			fmt.Println()
		}
	}

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
		fmt.Printf("  RPS: %d\n", test.RequestsPerSecond)
		fmt.Printf("  Threads: %d\n", test.Threads)
		fmt.Printf("  Duration: %ds\n", test.RunSeconds)
		if test.WarmupSeconds > 0 {
			fmt.Printf("  Warmup: %ds (ramp 0 → %d RPS)\n", test.WarmupSeconds, test.RequestsPerSecond)
		}

		if len(test.Steps) > 0 {
			fmt.Printf("  Steps: %d\n", len(test.Steps))
			for j, step := range test.Steps {
				label := step.Name
				if label == "" {
					label = fmt.Sprintf("step-%d", j+1)
				}
				fmt.Printf("    %d. %s %s %s\n", j+1, label, step.Method, step.Path)
				printAssertSummary(step.Assert, "       ")
			}
		} else {
			fmt.Printf("  Path: %s\n", test.Path)
			fmt.Printf("  Method: %s\n", test.Method)
			printAssertSummary(test.Assert, "    ")
		}
	}

	// Validate placeholders
	eval := placeholders.NewEvaluator(cfg)
	defer eval.Close()
	fmt.Println("\n=== Placeholder Validation ===")

	for _, test := range cfg.Tests {
		if err := validateRequestPlaceholders(eval, test.Headers, test.Body, test.Name); err != nil {
			return err
		}
		for j, step := range test.Steps {
			scope := fmt.Sprintf("%s/step[%d]", test.Name, j)
			if err := validateRequestPlaceholders(eval, step.Headers, step.Body, scope); err != nil {
				return err
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
		if len(test.Steps) > 0 {
			fmt.Printf("  Steps: %d (sequential per iteration)\n", len(test.Steps))
			for j, step := range test.Steps {
				label := step.Name
				if label == "" {
					label = fmt.Sprintf("step-%d", j+1)
				}
				fmt.Printf("    %d. %s %s %s\n", j+1, label, step.Method, step.Path)
			}
		} else {
			fmt.Printf("  Path: %s %s\n", test.Method, test.Path)
		}
		if test.WarmupSeconds > 0 {
			fmt.Printf("  Duration: %ds (+%ds warmup)\n", test.RunSeconds, test.WarmupSeconds)
		} else {
			fmt.Printf("  Duration: %ds\n", test.RunSeconds)
		}
		fmt.Printf("  Requests: %d total, %d success, %d failures\n",
			metrics.TotalRequests, metrics.SuccessCount, metrics.FailureCount)

		// Error breakdown (safe to read directly — metrics.Stop() already called)
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

		// Assertions — per-step configs can't be aggregated meaningfully across
		// all requests since each step may carry its own asserts, so for
		// multi-step tests we only print the declared asserts and fall back to
		// the overall passed/failed signal on the test as a whole.
		if len(test.Steps) > 0 {
			hasAnyAssert := false
			for _, s := range test.Steps {
				if s.Assert != nil {
					hasAnyAssert = true
					break
				}
			}
			if hasAnyAssert {
				fmt.Printf("  Assertions (per step):\n")
				for j, step := range test.Steps {
					if step.Assert == nil {
						continue
					}
					label := step.Name
					if label == "" {
						label = fmt.Sprintf("step-%d", j+1)
					}
					fmt.Printf("    %s:\n", label)
					printAssertSummary(step.Assert, "      ")
				}
				if metrics.AssertionFailures > 0 {
					fmt.Printf("    Assertion failures recorded: %d\n", metrics.AssertionFailures)
				}
			}
		} else if test.Assert != nil {
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
				for _, lat := range metrics.Latencies {
					if lat > time.Duration(test.Assert.MaxLatencyMs)*time.Millisecond {
						violations++
					}
				}
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

// printAssertSummary writes a short human-readable description of an assertion
// using the supplied indent prefix. A nil assertion prints nothing.
func printAssertSummary(a *config.Assertion, indent string) {
	if a == nil {
		return
	}
	if a.StatusCode != 0 {
		fmt.Printf("%s- Status Code: %d\n", indent, a.StatusCode)
	}
	if a.BodyContains != "" {
		fmt.Printf("%s- Body Contains: %s\n", indent, a.BodyContains)
	}
	if a.BodyEquals != "" {
		fmt.Printf("%s- Body Equals: %s\n", indent, a.BodyEquals)
	}
	if a.BodyNotEquals != "" {
		fmt.Printf("%s- Body Not Equals: %s\n", indent, a.BodyNotEquals)
	}
	if a.MaxLatencyMs > 0 {
		fmt.Printf("%s- Max Latency: %dms\n", indent, a.MaxLatencyMs)
	}
}

// validateRequestPlaceholders evaluates every placeholder occurring in the
// given headers map and body. scope is used to annotate error messages so
// callers can tell the failure site apart (e.g. "test/step[2]").
func validateRequestPlaceholders(eval *placeholders.Evaluator, headers map[string]string, body string, scope string) error {
	for key, value := range headers {
		if strings.Contains(value, "{{") {
			if _, err := eval.Evaluate(value); err != nil {
				return fmt.Errorf("invalid placeholder in %s header %s: %w", scope, key, err)
			}
		}
	}
	if strings.Contains(body, "{{") {
		if _, err := eval.Evaluate(body); err != nil {
			return fmt.Errorf("invalid placeholder in %s body: %w", scope, err)
		}
	}
	return nil
}
