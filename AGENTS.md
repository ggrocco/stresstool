# AGENTS.md

This file provides guidance to AI coding agents working with code in this repository.

## Build & Development Commands

```bash
# Build (dev)
go build -o stresstool ./cmd/stresstool

# Build with version info
./scripts-build.sh              # auto-detects version from git tags
./scripts-build.sh v2.0.0       # explicit version
./scripts-build.sh v2.0.0 bin/  # explicit version + output path

# Install globally
go install ./cmd/stresstool

# Release (triggered by pushing a tag: git tag v1.x.x && git push origin v1.x.x)
# GoReleaser cross-compiles for darwin/linux/windows (amd64+arm64) via GitHub Actions
# Local dry-run: goreleaser release --snapshot --clean

# Run tests
go test ./...
go test -race ./...              # with race detector
go test ./internal/runner         # single package

# Lint / format
go vet ./...
go fmt ./...

# Dependency audit + vulnerability check
./scripts-deps-check.sh
./scripts-deps-check.sh --upgrade  # upgrade deps first

# Protobuf generation (requires buf CLI)
buf generate
```

## What This Is

A distributed HTTP stress testing tool in Go. Supports two modes:
- **Standalone**: `stresstool run -f config.yaml` -- single process, no networking
- **Distributed**: Controller loads config and coordinates; worker nodes connect via TCP, receive test specs, execute, and report back

## Architecture

```
cmd/stresstool/main.go          -- CLI entry point (cobra), wires flags to internal/cli
internal/
  cli/
    cli.go                      -- standalone runner
    controller.go               -- controller: TCP listener, state machine, web UI, result aggregation
    node.go                     -- worker: connects to controller, executes tests, reports results
    message.go                  -- message send/receive helpers
    web/                        -- embedded web UI (HTML/CSS/JS) served by controller --web flag
  config/config.go              -- YAML config parsing, validation, node-specific overrides
  runner/
    runner.go                   -- HTTP execution engine: thread pool, rate limiting, progress reporting
    metrics.go                  -- channel-based metrics aggregation (single goroutine owns mutable state)
    asserts.go                  -- response assertions (status code, body matching, latency)
  placeholders/placeholders.go  -- {{ }} placeholder evaluation using goja JS runtime (single VM goroutine)
  protocol/protocol.go          -- typed JSON message envelope for controller-node TCP communication
  version/version.go            -- build version/commit/date injected via ldflags
proto/api/v1/payload.proto      -- protobuf schema (future gRPC migration path)
terraform/{aws,gcp,azure}/      -- cloud deployment configs
```

## Key Design Patterns

- **Channel-based concurrency everywhere** -- mutexes replaced with channel-serialized goroutines (metrics aggregator, placeholder evaluator, controller state manager)
- **Placeholder evaluator** runs a single goja (ES5 JS) VM goroutine; all evaluation requests go through `evalChan` for thread safety
- **Controller state** is managed by a dedicated goroutine processing events (node connections, progress, results) via channels; CLI and web UI query state through `queryChan`
- **Protocol**: JSON messages over persistent TCP connections using `Message{Type, Data json.RawMessage}` envelope. Message types: hello, test_spec, ready, start_tests, progress, test_result, complete, error

## Configuration

YAML-based (see `example-config.yaml`). Key features:
- `funcs`: custom shell commands callable as `{{ funcname() }}` placeholders
- `tests[].nodes`: per-node overrides for `requests_per_second`, `threads`
- Built-in placeholders: `{{ uuid() }}`, `{{ now() }}`, `{{ js('expr') }}`
- Assertions: `status_code`, `body_contains`, `body_equals`, `body_not_equals`, `max_latency_ms`

## Dependencies

Go 1.26.1+. Core deps: cobra (CLI), yaml.v3 (config), goja (JS eval), google/uuid, golang.org/x/time (rate limiting).
