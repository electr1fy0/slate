#!/usr/bin/env bash
# Local API development stack: moto-backed DynamoDB + uvicorn with reload.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOTO_PORT="${MOTO_PORT:-5000}"
API_PORT="${API_PORT:-8000}"
REGION="${AWS_DEFAULT_REGION:-ap-south-1}"

if [ -x "$ROOT/.venv/bin/python" ]; then
  PY="$ROOT/.venv/bin/python"
else
  PY="python3"
fi

export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-testing}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-testing}"
export AWS_DEFAULT_REGION="$REGION"

MOTO_PID=""
cleanup() {
  if [ -n "$MOTO_PID" ]; then
    kill "$MOTO_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

"$PY" -m moto.server -H 127.0.0.1 -p "$MOTO_PORT" >/dev/null 2>&1 &
MOTO_PID=$!

for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:$MOTO_PORT/moto-api/" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

echo "moto DynamoDB on :$MOTO_PORT"

"$PY" - "$MOTO_PORT" <<'PYEOF'
import sys

import boto3

port = sys.argv[1]
client = boto3.client("dynamodb", region_name="ap-south-1",
                      endpoint_url=f"http://127.0.0.1:{port}")
try:
    client.create_table(
        TableName="continuum",
        KeySchema=[
            {"AttributeName": "pk", "KeyType": "HASH"},
            {"AttributeName": "sk", "KeyType": "RANGE"},
        ],
        AttributeDefinitions=[
            {"AttributeName": "pk", "AttributeType": "S"},
            {"AttributeName": "sk", "AttributeType": "S"},
        ],
        BillingMode="PAY_PER_REQUEST",
    )
    print("created table 'continuum'")
except client.exceptions.ResourceInUseException:
    print("table 'continuum' already exists")
PYEOF

ROLE_FILE="${TMPDIR:-/tmp}/slate-dev-role"
mkdir -p "$(dirname "$ROLE_FILE")"
[ -f "$ROLE_FILE" ] || printf 'active' > "$ROLE_FILE"

echo "uvicorn API on :$API_PORT (role file $ROLE_FILE)"

TABLE_NAME=continuum \
DDB_ENDPOINT_MODE="http://127.0.0.1:$MOTO_PORT/" \
CONTINUUM_ROLE_FILE="$ROLE_FILE" \
ADMIN_TOKEN="${ADMIN_TOKEN:-continuum-demo-token}" \
CORS_ORIGINS="${CORS_ORIGINS:-http://localhost:3000,http://127.0.0.1:3000}" \
DISK_THRESHOLD_PCT=99 \
exec "$PY" -m uvicorn main:app --host 127.0.0.1 --port "$API_PORT" --reload --app-dir "$ROOT/app"
