#!/usr/bin/env bash
set -euo pipefail

TASK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
TASK_WORK="$(mktemp -d)"
trap 'rm -rf "$TASK_WORK"' EXIT
mkdir -p "$TASK_ROOT/build/demo"
cd "$TASK_ROOT"

for name in router demo-control demo-verifier demo-monitor demo-reconciler; do
  mkdir -p "$TASK_WORK/$name"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$TASK_WORK/$name/bootstrap" "./cmd/$name"
  chmod 0755 "$TASK_WORK/$name/bootstrap"
  python3 scripts/package_demo.py zip "$TASK_WORK/$name/bootstrap" "build/demo/$name.zip"
  echo "Built $name"
done

mkdir -p "$TASK_WORK/app"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$TASK_WORK/app/continuum-api" ./cmd/api
cp deploy/continuum.service "$TASK_WORK/app/continuum.service"
python3 scripts/package_demo.py tar "$TASK_WORK/app" build/demo/app.tar.gz
echo "Built app.tar.gz for both application instances"
