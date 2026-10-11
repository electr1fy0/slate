#!/usr/bin/env bash
set -euo pipefail

TASK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TASK_TF="${1:-terraform}"
cd "$TASK_ROOT"
mkdir -p .demo
umask 077
"$TASK_TF" -chdir=infra/demo output -json > .demo/aws-output.json
TASK_API_URL="$(python3 - <<'PY'
import json,pathlib
print(json.loads(pathlib.Path('.demo/aws-output.json').read_text())['api_url']['value'])
PY
)"
(cd web && VITE_API_ORIGIN="$TASK_API_URL" bun run build)
"$TASK_TF" -chdir=infra/demo plan -out=web.tfplan
"$TASK_TF" -chdir=infra/demo apply web.tfplan
TASK_DISTRIBUTION_ID="$(python3 - <<'PY'
import json,pathlib
print(json.loads(pathlib.Path('.demo/aws-output.json').read_text())['distribution_id']['value'])
PY
)"
# A cached SPA shell can reference removed asset filenames after a new build.
aws cloudfront create-invalidation --distribution-id "$TASK_DISTRIBUTION_ID" --paths '/*' > .demo/invalidation.json
echo "AWS URLs and private demo credentials are in .demo/aws-output.json. Keep that file private."
"$TASK_TF" -chdir=infra/demo output -raw frontend_url
echo
