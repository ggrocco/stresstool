#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo "dev")}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo "none")"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
MODULE="stresstool/internal/version"
OUTPUT="${2:-stresstool}"

LDFLAGS="-s -w"
LDFLAGS+=" -X ${MODULE}.Version=${VERSION}"
LDFLAGS+=" -X ${MODULE}.Commit=${COMMIT}"
LDFLAGS+=" -X ${MODULE}.BuildDate=${BUILD_DATE}"

echo "Building ${OUTPUT} ${VERSION} (${COMMIT}) ..."
go build -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/stresstool
echo "Done: ${OUTPUT}"
