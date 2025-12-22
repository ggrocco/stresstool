# stresstool

A command-line HTTP stress test tool written in Go that reads YAML configuration files and executes concurrent HTTP requests with assertions.

## Features

- **YAML-Driven Configuration**: Define tests in a simple YAML format
- **Dynamic Placeholders**: Use JavaScript functions and custom CLI commands to generate dynamic values
- **Concurrent Execution**: Configurable worker threads and requests per second
- **Real-time Progress**: Live progress indicators showing RPS, requests, and failures
- **Assertions**: Validate status codes, response body content, and latency thresholds
- **Comprehensive Metrics**: Min/max/avg latency, percentiles (P95, P99), and detailed statistics

## Installation

### Build from Source

```bash
git clone <repository-url>
cd stresstool
go build -o stresstool ./cmd/stresstool
```

### Install Globally

```bash
go install ./cmd/stresstool
```

## Usage

### Basic Usage

```bash
stresstool run -f config.yaml
```

### Flags

- `-f, --file string`: Path to YAML configuration file (required)
- `--verbose`: Print detailed logs for each request
- `--dry-run`: Validate configuration and show planned tests without executing HTTP calls
- `--parallel`: Run all specs in parallel

### Example

```bash
# Validate configuration
stresstool run -f config.yaml --dry-run

# Run stress tests
stresstool run -f config.yaml

# Run with verbose output
stresstool run -f config.yaml --verbose

# Run all specs in parallel
stresstool run -f config.yaml --parallel
```

## Configuration Format

### YAML Structure

```yaml
funcs:
  - name: token
    cmd: ["./cli/token", "arg1", "arg2"]
  - name: generateId
    cmd: ["node", "scripts/generate.js"]

tests:
  - name: api_login_test
    path: "https://api.example.com/login"
    method: "POST"
    requests_per_second: 10
    threads: 4
    run_seconds: 300

    headers:
      Authorization: "{{ token() }}"
      X-Request-Id: "{{ uuid() }}"
      X-Timestamp: "{{ now() }}"
      Content-Type: "application/json"

    body: >
      {
        "username": "user@example.com",
        "password": "secret123"
      }

    assert:
      status_code: 200
      body_contains: "success"
      max_latency_ms: 500
```

### Configuration Fields

#### `funcs` (optional)

Array of custom functions that can be called via placeholders.

- `name`: Function identifier used in placeholders (e.g., `{{ token() }}`)
- `cmd`: Array of strings executed as a subprocess; stdout is captured and used as the return value

#### `tests` (required)

Array of HTTP stress tests to execute.

- `name`: Test identifier (optional but recommended)
- `path`: Full URL to hit (required)
- `method`: HTTP method (default: "GET")
- `requests_per_second`: Target requests per second (required, must be > 0)
- `threads`: Number of concurrent worker threads (required, must be > 0)
- `run_seconds`: Total duration to run the test in seconds (required, must be > 0)
- `headers`: Map of HTTP headers (may include placeholders)
- `body`: Request body string (may include placeholders)
- `assert`: Assertion configuration (see below)

#### `assert` (optional)

Defines assertions to validate responses.

- `status_code`: Expected HTTP status code
- `body_contains`: Substring that must appear in the response body (supports placeholders and custom funcs)
- `body_equals`: Full response body must match this value exactly (supports placeholders and custom funcs)
- `body_not_equals`: Full response body must not match this value (supports placeholders and custom funcs)
- `max_latency_ms`: Maximum allowed latency in milliseconds

Assertion strings are evaluated with the same placeholder engine as headers and bodies, so you can embed expressions like `{{ token() }}` or `{{ js('"ready"') }}`.

### Placeholders

Placeholders are delimited by double curly braces and are evaluated per request.

#### Built-in Functions

- `{{ now() }}`: Current timestamp as ISO 8601 string
- `{{ uuid() }}`: Randomly generated UUID string
- `{{ js("new Date().getTime()") }}`: Execute arbitrary JavaScript code

#### Custom Functions

Custom functions defined in `funcs` can be called:

```yaml
funcs:
  - name: token
    cmd: ["./scripts/get-token.sh"]

tests:
  - name: test
    headers:
      Authorization: "Bearer {{ token() }}"
```

#### JavaScript Evaluation

You can execute JavaScript expressions:

```yaml
headers:
  X-Timestamp: "{{ js('Date.now().toString()') }}"
  X-Random: "{{ js('Math.random().toString(36)') }}"
```

## Example Output

### During Execution

```
→ api_login_test: 45s elapsed - 450 requests, 10.0 RPS, 2 failures
→ api_search_test: 30s elapsed - 300 requests, 10.0 RPS, 0 failures
```

### Final Summary

```
================================================================================
TEST SUMMARY
================================================================================

Test: api_login_test
  Path: POST https://api.example.com/login
  Duration: 300s
  Requests: 3000 total, 2998 success, 2 failures
  Latency:
    Min: 45ms
    Avg: 120ms
    Max: 450ms
    P95: 250ms
    P99: 380ms
  Assertions:
    Status Code 200: ✓ PASSED
    Body Contains 'success': ✓ PASSED
    Max Latency 500ms: ✓ PASSED
  Result: ✓ PASSED

================================================================================
OVERALL RESULT: ✓ ALL TESTS PASSED
================================================================================
```

## Exit Codes

- `0`: All tests passed
- `1`: One or more tests failed or an error occurred

## Error Handling

- Configuration validation errors are reported before execution
- Invalid placeholders cause immediate failure
- Failed function executions (custom `funcs`) are treated as request failures
- Network errors and timeouts are tracked as failures
- Assertion failures are reported in the summary

## Best Practices

1. **Start Small**: Begin with low RPS and short durations to validate your configuration
2. **Use Dry-Run**: Always validate your config with `--dry-run` before running tests
3. **Monitor Resources**: Ensure your target server can handle the load
4. **Set Realistic Assertions**: Use appropriate latency thresholds based on your infrastructure
5. **Custom Functions**: Keep custom function execution time short to avoid rate limiting issues

## Limitations

- Response bodies are limited to 1MB for assertion checking
- Custom function execution has a 30-second timeout
- JavaScript evaluation uses the goja engine (ECMAScript 5.1 compatible)


