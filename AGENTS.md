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

# Protobuf generation (requires Buf CLI: brew install bufbuild/buf/buf on macOS)
buf generate

# Docker: controller + two nodes (needs Docker Desktop / engine)
docker compose up --build                              # manual Start in browser :8091
docker compose --profile auto-start up --build        # optional: curl POST /api/start
```

## What This Is

A distributed HTTP stress testing tool in Go. Supports two modes:
- **Standalone**: `stresstool run -f config.yaml` -- single process, no networking
- **Distributed**: Controller loads config and coordinates; worker nodes connect via **gRPC** (bidirectional `Session` stream), receive test specs, execute, and report back. Use `--insecure` (default) for plaintext gRPC; pass `--tls-cert` / `--tls-key` / `--tls-ca` for TLS or mTLS.

## Architecture

```
cmd/stresstool/main.go          -- CLI entry point (cobra), wires flags to internal/cli
internal/
  auth/auth.go                  -- auth header resolution (basic, bearer, api_key, oauth2); channel-based OAuth2 token cache
  cli/
    cli.go                      -- standalone runner
    controller.go               -- controller: state machine, web UI, result aggregation
    controller_grpc.go          -- gRPC server: StressTestService Session handler, per-node send queue
    node.go                     -- worker: gRPC client session, executes tests, reports results
    tls.go                      -- TLS helpers for controller and node
    web/                        -- embedded web UI (HTML/CSS/JS) served by controller --web flag
  config/config.go              -- YAML config parsing, validation, auth config, node-specific overrides
  runner/
    runner.go                   -- HTTP execution engine: thread pool, rate limiting, progress reporting, auth header injection
    metrics.go                  -- channel-based metrics aggregation (single goroutine owns mutable state)
    asserts.go                  -- response assertions (status code, body matching, latency)
  placeholders/placeholders.go  -- {{ }} placeholder evaluation using goja JS runtime (single VM goroutine)
  protocol/protocol.go          -- typed JSON message types (standalone / docs); distributed path uses protobuf
  protocol/convert.go           -- config and TestResult <-> protobuf conversion (includes auth round-trip)
  protocol/payloadpb/           -- generated Go from proto (buf generate)
  version/version.go            -- build version/commit/date injected via ldflags
proto/api/v1/payload.proto      -- protobuf schema + StressTestService (gRPC)
terraform/{aws,gcp,azure}/      -- cloud deployment configs
```

## Key Design Patterns

- **Channel-based concurrency everywhere** -- mutexes replaced with channel-serialized goroutines (metrics aggregator, placeholder evaluator, controller state manager, OAuth2 token cache)
- **Placeholder evaluator** runs a single goja (ES5 JS) VM goroutine; all evaluation requests go through `evalChan` for thread safety
- **Controller state** is managed by a dedicated goroutine processing events (node connections, progress, results) via channels; CLI and web UI query state through `queryChan`
- **Auth resolver** (`internal/auth/auth.go`) resolves auth config into HTTP headers per-request. OAuth2 token caching uses a channel-based goroutine (no mutex) -- callers send requests to `tokenCh`, the goroutine owns token state and responds with cached or freshly-fetched tokens.
- **Protocol (distributed)**: gRPC bidi stream `Session` with `NodeMessage` / `ControllerMessage` oneofs (protobuf). Controller serializes `Send` per stream via a buffered channel. JSON `protocol.Message` types remain for documentation parity with YAML-centric tooling.

## Configuration

YAML-based (see `example-config.yaml`). Key features:
- `auth`: hash keyed by auth type -- only one type per config. Supported: `jwt` (recommended), `basic_auth`, `bearer`, `api_key`, `oauth2_client_credentials`. All tests use it automatically; individual tests opt out with `auth: false`. Values support `{{ }}` placeholders. For `jwt`, the `header` and `payload` blocks are merged on top of defaults (`{alg: HS256, typ: JWT}` / `{iat, exp}`) so only overrides need to be specified; algorithms: HS256/HS384/HS512.
- `funcs`: custom shell commands callable as `{{ funcname() }}` placeholders
- `tests[].nodes`: per-node overrides for `requests_per_second`, `threads`
- Built-in placeholders: `{{ uuid() }}`, `{{ now() }}`, `{{ js('expr') }}`
- Assertions: `status_code`, `body_contains`, `body_equals`, `body_not_equals`, `max_latency_ms`

## Dependencies

Go 1.26.1+. Core deps: cobra (CLI), yaml.v3 (config), goja (JS eval), google/uuid, golang.org/x/time (rate limiting), google.golang.org/grpc, google.golang.org/protobuf.
