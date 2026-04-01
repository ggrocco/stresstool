#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./scripts-deps-check.sh            # check vulnerabilities and list outdated modules
#   ./scripts-deps-check.sh --upgrade  # attempt to upgrade dependencies then verify

upgrade=false
if [[ "${1:-}" == "--upgrade" ]]; then
  upgrade=true
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required" >&2
  exit 1
fi

if $upgrade; then
  echo "==> Upgrading module dependencies"
  go get -u ./...
  go mod tidy
fi

echo "==> Listing outdated dependencies"
go list -m -u all || {
  echo "warning: unable to query for updates (network/proxy restrictions?)" >&2
}

if ! command -v govulncheck >/dev/null 2>&1; then
  echo "==> Installing govulncheck"
  go install golang.org/x/vuln/cmd/govulncheck@latest
fi

echo "==> Running govulncheck"
govulncheck ./...
