#!/usr/bin/env bash
set -euo pipefail
TASK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TASK_MODE="${1:?Use paused or running}"
TASK_TF="${2:-terraform}"
case "$TASK_MODE" in paused|running) ;; *) echo "Use paused or running" >&2; exit 1 ;; esac
cd "$TASK_ROOT"
umask 077
python3 - "$TASK_MODE" <<'PY'
import json,pathlib,sys
p=pathlib.Path('infra/demo/runtime.auto.tfvars.json')
values=json.loads(p.read_text()) if p.exists() else {}
values.update(instances_running=sys.argv[1]=='running', monitor_enabled=sys.argv[1]=='running')
p.write_text(json.dumps(values,indent=2)+'\n')
p.chmod(0o600)
PY
"$TASK_TF" -chdir=infra/demo plan -out=runtime.tfplan
"$TASK_TF" -chdir=infra/demo apply runtime.tfplan
