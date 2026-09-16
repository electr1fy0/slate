#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_PORT="${API_PORT:-8000}"
REGION="${AWS_DEFAULT_REGION:-ap-south-1}"

export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-testing}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-testing}"
export AWS_DEFAULT_REGION="$REGION"
export TABLE_NAME="${TABLE_NAME:-continuum}"
export LISTEN_ADDR="${LISTEN_ADDR:-127.0.0.1:$API_PORT}"
if [[ -z "${DDB_ENDPOINT_MODE:-}" && -z "${STORE_BACKEND:-}" ]]; then
  export STORE_BACKEND="memory"
fi
export ADMIN_TOKEN="${ADMIN_TOKEN:-continuum-demo-token}"
export BOOTSTRAP_TEACHER_EMAIL="${BOOTSTRAP_TEACHER_EMAIL:-teacher@slate.local}"
export BOOTSTRAP_TEACHER_PASSWORD="${BOOTSTRAP_TEACHER_PASSWORD:-teach-me-slate}"
export CORS_ORIGINS="${CORS_ORIGINS:-http://localhost:3000,http://127.0.0.1:3000}"
export DISK_THRESHOLD_PCT="${DISK_THRESHOLD_PCT:-99}"
export LOAD_THRESHOLD="${LOAD_THRESHOLD:-64}"
export JWT_SECRET="${JWT_SECRET:-dev-only-insecure-secret-change-me-32b}"

if [[ "${STORE_BACKEND:-}" == "memory" ]]; then
  echo "Using in-memory data store (set STORE_BACKEND=dynamodb for DynamoDB)."
elif [[ -z "${DDB_ENDPOINT_MODE:-}" ]]; then
  echo "DDB_ENDPOINT_MODE is unset; configure DynamoDB Local/LocalStack or AWS credentials before starting the API." >&2
fi

echo "Go API on http://$LISTEN_ADDR"
exec go run "$ROOT/cmd/api"
