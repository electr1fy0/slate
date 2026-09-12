#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
BUILD_DIR="$ROOT/build/lambda"
APP_BUILD_DIR="$ROOT/build/app"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$BUILD_DIR"
mkdir -p "$APP_BUILD_DIR"

build_lambda() {
  local name="$1"
  local package="$2"
  local package_dir="$WORK_DIR/$name"
  mkdir -p "$package_dir"
  echo ">> building $name (linux/amd64)"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" build \
    -trimpath -ldflags='-s -w' -o "$package_dir/bootstrap" "$ROOT/$package"
  chmod 0755 "$package_dir/bootstrap"
  rm -f "$BUILD_DIR/$name.zip"
  (cd "$package_dir" && zip -q -X "$BUILD_DIR/$name.zip" bootstrap)
}

build_lambda "health-verifier" "cmd/health-verifier"
build_lambda "failover-ops" "cmd/failover-ops"
build_lambda "reconciler" "cmd/reconciler"

echo ">> building continuum-api (linux/amd64)"
APP_PACKAGE_DIR="$WORK_DIR/app"
mkdir -p "$APP_PACKAGE_DIR"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" build \
  -trimpath -ldflags='-s -w' -o "$APP_PACKAGE_DIR/continuum-api" "$ROOT/cmd/api"
chmod 0755 "$APP_PACKAGE_DIR/continuum-api"
cp "$ROOT/deploy/continuum.service" "$APP_PACKAGE_DIR/continuum.service"
cp "$ROOT/deploy/set_role.sh" "$APP_PACKAGE_DIR/set_role.sh"
chmod 0755 "$APP_PACKAGE_DIR/set_role.sh"
tar -czf "$APP_BUILD_DIR/app.tar.gz" -C "$APP_PACKAGE_DIR" .

echo ">> Go Lambda packages written to $BUILD_DIR"
echo ">> Go API package written to $APP_BUILD_DIR/app.tar.gz"
