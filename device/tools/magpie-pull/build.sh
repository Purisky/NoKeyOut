#!/usr/bin/env bash
# build.sh —— 构建 magpie-pull（静态，设备上原生构建，随 release 发布）
set -euo pipefail
cd "$(dirname "$0")"
GOOS=linux GOARCH="${GOARCH:-$(go env GOARCH)}" CGO_ENABLED=0 go vet ./...
GOOS=linux GOARCH="${GOARCH:-$(go env GOARCH)}" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o magpie-pull .
ls -la magpie-pull
