#!/usr/bin/env bash
set -euo pipefail

TASK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$TASK_ROOT"
mkdir -p .demo
umask 077
if [[ ! -f .demo/local.env ]]; then
  python3 - <<'PY'
import pathlib,secrets
p=pathlib.Path('.demo/local.env')
p.write_text('AWS_REGION=ap-southeast-2\nSTORE_BACKEND=memory\n'
             'ADMIN_TOKEN='+secrets.token_hex(20)+'\nJWT_SECRET='+secrets.token_hex(32)+'\n'
             'BOOTSTRAP_TEACHER_EMAIL=faculty@continuum.test\n'
             'BOOTSTRAP_TEACHER_PASSWORD='+secrets.token_hex(12)+'\n')
p.chmod(0o600)
PY
fi
set -a
source .demo/local.env
set +a
# Both demo backends share the laptop: unrelated compiler/app load must not
# look like a correlated backend failure. AWS retains the real host threshold.
export LOAD_THRESHOLD="${DEMO_LOAD_THRESHOLD:-1000}"
go build -o .demo/continuum-demo ./cmd/demo
.demo/continuum-demo > .demo/local.log 2>&1 &
TASK_API_PID=$!
cleanup() { kill "$TASK_API_PID" 2>/dev/null || true; }
trap cleanup EXIT INT TERM
TASK_API_READY=false
for ((i=0;i<60;i++)); do
  if curl -fsS http://127.0.0.1:8080/health/live >/dev/null 2>&1; then TASK_API_READY=true; break; fi
  if ! kill -0 "$TASK_API_PID" 2>/dev/null; then cat .demo/local.log; exit 1; fi
  sleep 0.2
done
if [[ "$TASK_API_READY" != true ]]; then cat .demo/local.log; echo "Demo API did not become ready." >&2; exit 1; fi
TASK_WEB_PORT="${DEMO_WEB_PORT:-3000}"
echo "Open http://localhost:${TASK_WEB_PORT} — /ops is the recovery cockpit."
echo "Local teacher credentials and cockpit token: .demo/local.env (private, ignored by Git)."
echo "This rehearsal uses two local HTTP servers and shared memory. AWS uses EC2 and DynamoDB."
cd web
CONTINUUM_DEMO=1 API_ORIGIN=http://127.0.0.1:8080 bun run dev --host 127.0.0.1 --port "$TASK_WEB_PORT" --strictPort
