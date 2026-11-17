#!/usr/bin/env sh
set -eu
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$task_root"
python3 scripts/check_version.py
test "$(go env GOVERSION)" = "go$(cat .go-version)"
test "$(zig version)" = "$(cat .zig-version)"
mkdir -p build
env GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=1 \
  CC='zig cc -target x86_64-linux-gnu.2.34' \
  go build -trimpath -buildmode=c-shared -ldflags='-s -w' \
  -o build/antigravity-prompt-bridge.so .
