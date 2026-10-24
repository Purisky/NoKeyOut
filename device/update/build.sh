#!/usr/bin/env bash
# build.sh —— 构建 nokeyout-update（静态 arm64）
set -euo pipefail
cd "$(dirname "$0")"
ARCH="${ARCH:-arm64}"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags="-s -w" -o nokeyout-update .
ls -l nokeyout-update
