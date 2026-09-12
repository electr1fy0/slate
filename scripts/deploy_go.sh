#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAMP="$(date +%Y%m%d%H%M%S)"

param() { aws ssm get-parameter --name "$1" --query Parameter.Value --output text; }

"$ROOT/scripts/build_go.sh"

BUNDLE_PRI="$(param /continuum/bucket/bundle_primary)"
BUNDLE_STB="$(param /continuum/bucket/bundle_standby)"
INST_PRI="$(param /continuum/primary/instance_id)"
INST_STB="$(param /continuum/standby/instance_id)"

echo ">> uploading Go API bundle"
aws s3 cp "$ROOT/build/app/app.tar.gz" "s3://$BUNDLE_PRI/bundles/app.tar.gz"
aws s3 cp "$ROOT/build/app/app.tar.gz" "s3://$BUNDLE_STB/bundles/app.tar.gz"

deploy_instance() {
  local instance="$1"
  local bucket="$2"
  local commands
  commands="$(jq -nc --arg bucket "$bucket" --arg stamp "$STAMP" '[
    ("aws s3 cp s3://" + $bucket + "/bundles/app.tar.gz /tmp/app-" + $stamp + ".tar.gz"),
    "rm -rf /opt/continuum/app.new && mkdir -p /opt/continuum/app.new",
    ("tar -xzf /tmp/app-" + $stamp + ".tar.gz -C /opt/continuum/app.new"),
    "chmod +x /opt/continuum/app.new/continuum-api /opt/continuum/app.new/set_role.sh",
    "rm -rf /opt/continuum/app.old && mv /opt/continuum/app /opt/continuum/app.old 2>/dev/null || true",
    "mv /opt/continuum/app.new /opt/continuum/app",
    "cp /opt/continuum/app/continuum.service /etc/systemd/system/continuum.service",
    "systemctl daemon-reload && systemctl restart continuum",
    "sleep 2 && curl -fsS http://localhost/health/live || (journalctl -u continuum -n 30 --no-pager; exit 1)"
  ]')"
  local command_id
  command_id="$(aws ssm send-command \
    --instance-ids "$instance" \
    --document-name AWS-RunShellScript \
    --comment "Continuum Go deploy $STAMP" \
    --parameters "{\"commands\":$commands}" \
    --query Command.CommandId --output text)"
  aws ssm wait command-executed --command-id "$command_id" --instance-id "$instance"
  aws ssm get-command-invocation --command-id "$command_id" --instance-id "$instance" \
    --query '{Status:Status,Output:StandardOutputContent,Error:StandardErrorContent}'
}

# Standby first preserves a tested rollback target while primary still serves.
deploy_instance "$INST_STB" "$BUNDLE_STB"
deploy_instance "$INST_PRI" "$BUNDLE_PRI"

echo ">> Go API deployed to standby and primary"
