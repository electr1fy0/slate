#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOTO_PORT="${MOTO_PORT:-5000}"
API_PORT="${API_PORT:-8000}"
REGION="${AWS_DEFAULT_REGION:-ap-south-1}"

if [ ! -x "$ROOT/.venv/bin/python" ]; then
  echo "missing .venv; run: python3 -m venv .venv && .venv/bin/pip install -r requirements-dev.txt" >&2
  exit 1
fi

export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-testing}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-testing}"
export AWS_DEFAULT_REGION="$REGION"

MOTO_PID=""
API_PID=""
cleanup() {
  [ -n "$API_PID" ] && kill "$API_PID" 2>/dev/null || true
  [ -n "$MOTO_PID" ] && kill "$MOTO_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

"$ROOT/.venv/bin/python" -m moto.server -H 127.0.0.1 -p "$MOTO_PORT" >/dev/null 2>&1 &
MOTO_PID=$!
for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:$MOTO_PORT/moto-api/" >/dev/null 2>&1 && break
  sleep 0.25
done

"$ROOT/.venv/bin/python" - "$MOTO_PORT" <<'PY'
import sys
import boto3

client = boto3.client("dynamodb", region_name="ap-south-1", endpoint_url=f"http://127.0.0.1:{sys.argv[1]}")
try:
    client.create_table(
        TableName="continuum",
        KeySchema=[{"AttributeName": "pk", "KeyType": "HASH"}, {"AttributeName": "sk", "KeyType": "RANGE"}],
        AttributeDefinitions=[{"AttributeName": "pk", "AttributeType": "S"}, {"AttributeName": "sk", "AttributeType": "S"}],
        BillingMode="PAY_PER_REQUEST",
    )
except client.exceptions.ResourceInUseException:
    pass
PY

ROLE_FILE="${TMPDIR:-/tmp}/slate-go-dev-role"
[ -f "$ROLE_FILE" ] || printf active > "$ROLE_FILE"

TABLE_NAME=continuum \
DDB_ENDPOINT_MODE="http://127.0.0.1:$MOTO_PORT/" \
CONTINUUM_ROLE_FILE="$ROLE_FILE" \
LISTEN_ADDR="127.0.0.1:$API_PORT" \
ADMIN_TOKEN="${ADMIN_TOKEN:-continuum-demo-token}" \
BOOTSTRAP_TEACHER_EMAIL="${BOOTSTRAP_TEACHER_EMAIL:-teacher@slate.local}" \
BOOTSTRAP_TEACHER_PASSWORD="${BOOTSTRAP_TEACHER_PASSWORD:-teach-me-slate}" \
CORS_ORIGINS="${CORS_ORIGINS:-http://localhost:3000,http://127.0.0.1:3000}" \
DISK_THRESHOLD_PCT=99 LOAD_THRESHOLD=64 \
JWT_SECRET="${JWT_SECRET:-dev-only-insecure-secret-change-me-32b}" \
go run "$ROOT/cmd/api" &
API_PID=$!

for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:$API_PORT/health/live" >/dev/null 2>&1; then
    echo "Go API ready on http://127.0.0.1:$API_PORT"
    break
  fi
  sleep 0.5
done
wait "$API_PID"
