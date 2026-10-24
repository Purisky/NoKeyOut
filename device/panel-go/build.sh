#!/usr/bin/env bash
# build.sh —— 在设备上原生构建 panel（arm64 静态二进制，随 release 发布）
#
#   ./build.sh            # 产出 ./panel
#
# CGO_ENABLED=0：PAM 走 root helper（sudo -n nokeyout-auth），不链 libpam，
# 因此可完全静态构建——Buildroot/精简镜像同样适用。
set -euo pipefail
cd "$(dirname "$0")"
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o panel .
ls -la panel
file panel | sed 's/,.*BuildID.*//'
