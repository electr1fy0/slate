#!/usr/bin/env bash
set -euo pipefail

TASK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TASK_TF="${1:-terraform}"
cd "$TASK_ROOT"
mkdir -p .demo
umask 077

# This preflight never upgrades the project. The AWS CLI is for the person
# running this standalone script; the agent uses the AWS MCP for AWS actions.
aws freetier get-account-plan-state --region ap-southeast-2 > .demo/plan-state.json
python3 - <<'PY'
import json,pathlib
state=json.loads(pathlib.Path('.demo/plan-state.json').read_text())
if state.get('accountPlanType')!='FREE':
    raise SystemExit('Deploy stopped: this project must remain on the AWS Free plan.')
print('Free plan confirmed. Check the remaining credits in AWS Settings > Billing before deploying.')
PY

go test -race ./...
(cd web && bun run typecheck && bun run lint && bun run test && bun run build)
bash scripts/build_demo.sh
"$TASK_TF" -chdir=infra/demo init
"$TASK_TF" -chdir=infra/demo plan -out=demo.tfplan
"$TASK_TF" -chdir=infra/demo apply demo.tfplan
bash scripts/deploy_demo_web.sh "$TASK_TF"
echo "Monitoring follows the saved runtime state (disabled on first deploy). Use make demo-resume for a rehearsal and make demo-pause afterwards."
