#!/usr/bin/env bash
# build.sh —— 构建 C 版 PAM helper（设备本地；产物随 release 发布）
#
#   ./build.sh            # 产出 ./nokeyout-auth
#
# 构建依赖：cc + libpam 头文件（Debian/Ubuntu: apt install libpam0g-dev；
# Buildroot: 选 BR2_PACKAGE_LINUX_PAM）。
set -euo pipefail
cd "$(dirname "$0")"
cc -O2 -Wall -Wextra -o nokeyout-auth nokeyout-auth.c -lpam
ls -la nokeyout-auth
file nokeyout-auth | sed 's/,.*BuildID.*//'
