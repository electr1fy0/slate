#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAMP="$(date +%Y%m%d%H%M%S)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

param() { aws ssm get-parameter --name "$1" --query Parameter.Value --output text; }

BUNDLE_PRI="$(param /continuum/bucket/bundle_primary)"
BUNDLE_STB="$(param /continuum/bucket/bundle_standby)"
INST_PRI="$(param /continuum/primary/instance_id)"
INST_STB="$(param /continuum/standby/instance_id)"

echo ">> packaging app"
tar -czf "$WORK/app.tar.gz" -C "$ROOT" app

echo ">> uploading to both regional bundle buckets"
aws s3 cp "$WORK/app.tar.gz" "s3://$BUNDLE_PRI/bundles/app.tar.gz"
aws s3 cp "$WORK/app.tar.gz" "s3://$BUNDLE_STB/bundles/app.tar.gz"

DEPLOY_COMMANDS=(
  "aws s3 cp s3://$BUNDLE_PRI/bundles/app.tar.gz /tmp/app-$STAMP.tar.gz"
  "rm -rf /opt/continuum/app.new && mkdir -p /opt/continuum/app.new"
  "tar -xzf /tmp/app-$STAMP.tar.gz -C /opt/continuum/app.new --strip-components=1"
  "chmod +x /opt/continuum/app.new/set_role.sh"
  "rm -rf /opt/continuum/app.old && mv /opt/continuum/app /opt/continuum/app.old 2>/dev/null || true"
  "mv /opt/continuum/app.new /opt/continuum/app"
  "/opt/venv/bin/pip install -q -r /opt/continuum/app/requirements.txt"
  "systemctl restart continuum"
  "sleep 2 && curl -s http://localhost/health/live || journalctl -u continuum -n 20 --no-pager"
)

JSON_CMDS="$(printf '%s\n' "${DEPLOY_COMMANDS[@]}" | python3 -c 'import sys,json;print(json.dumps(sys.stdin.read().splitlines()))')"

for inst in "$INST_PRI" "$INST_STB"; do
  echo ">> deploying to $inst"
  cid="$(aws ssm send-command \
    --instance-ids "$inst" \
    --document-name "AWS-RunShellScript" \
    --comment "Continuum app deploy $STAMP" \
    --parameters "{\"commands\": $JSON_CMDS}" \
    --query Command.CommandId --output text)"
  echo "   command id: $cid (poll: aws ssm wait command-executed --command-id $cid --instance-id $inst)"
done

echo ">> done. verify with: python3 chaos/continuum_chaos.py status"
