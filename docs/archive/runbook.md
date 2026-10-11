> **Historical only — not deployable for the current demonstration.** The commands below refer to the retired cross-Region stack and must not be followed. Use the [current implementation runbook](../demo-runbook.md) for setup, lifecycle, and pause/resume.

# Continuum Runbook

## First-time setup

1. Configure AWS credentials (`aws configure` or environment) and confirm identity:
   `aws sts get-caller-identity`.
2. Set a billing alarm at $15 before creating resources.
3. Install Terraform: `brew install hashicorp/tap/hashicorp`.
4. Copy each `terraform.tfvars.example` to `terraform.tfvars`, then set the shared
   `admin_token` and `alert_email`. If using a domain, set `domain_name` in control.

## Deploy

```bash
make core        # primary region, data plane, and verifier
make standby     # standby region compute and verifier
make app-deploy  # Go API bundle -> both buckets -> SSM deploy
make control     # Route 53, Step Functions, Lambdas, dashboard
```

After instance bootstrap completes, export the endpoint and admin token:

```bash
export API_URL=https://<domain-or-primary-ip>
export ADMIN_TOKEN=<admin-token>
curl -fsS "$API_URL/health/live"
curl -fsS "$API_URL/health/deep"
curl -fsS -H "x-admin-token: $ADMIN_TOKEN" "$API_URL/admin/status"
```

Confirm scheduled verifiers publish `Continuum/Health{Role}` metrics in CloudWatch.

## Redeploying the app

```bash
make app-deploy
```

## Teardown between demo windows

```bash
make destroy-all    # control -> standby -> core
```

DynamoDB data and force-destroy buckets are intentionally removed by teardown.

## Manual operations

| Action | Command |
|---|---|
| Promote the current node manually | `curl -fsS -X POST "$API_URL/admin/promote" -H "x-admin-token: $ADMIN_TOKEN"` |
| Inject 30% errors | `curl -fsS -X POST "$API_URL/admin/chaos" -H "x-admin-token: $ADMIN_TOKEN" -H 'content-type: application/json' -d '{"error_pct":30}'` |
| Add 400 ms latency | `curl -fsS -X POST "$API_URL/admin/chaos" -H "x-admin-token: $ADMIN_TOKEN" -H 'content-type: application/json' -d '{"latency_ms":400}'` |
| Stall the database probe | `curl -fsS -X POST "$API_URL/admin/chaos" -H "x-admin-token: $ADMIN_TOKEN" -H 'content-type: application/json' -d '{"db_stall":true}'` |
| Reset chaos | `curl -fsS -X POST "$API_URL/admin/chaos/reset" -H "x-admin-token: $ADMIN_TOKEN"` |
| Run reconciler now | `aws lambda invoke --function-name continuum-reconciler --payload '{}' /tmp/continuum-reconcile.json` |
| Stop a node for a crash test | `aws ssm send-command --instance-ids <instance-id> --document-name AWS-RunShellScript --parameters commands='["sudo systemctl stop continuum"]'` |

After a failover test, reset chaos, promote the intended primary, and clear any
manual health-check inversion:

```bash
curl -fsS -X POST "$API_URL/admin/chaos/reset" -H "x-admin-token: $ADMIN_TOKEN"
curl -fsS -X POST "$API_URL/admin/promote" -H "x-admin-token: $ADMIN_TOKEN"
aws route53 update-health-check \
  --health-check-id "$(terraform -chdir=infra/control output -raw primary_health_check_id)" \
  --no-inverted
```

## Troubleshooting

**App not reachable on :80** — use `aws ssm send-command` to inspect
`journalctl -u continuum -n 50`, then rerun `make app-deploy` if the bundle is stale.

**Verifier metric absent** — inspect the verifier Lambda logs. Check IAM permissions,
the table name, target URL, and the security group rule allowing health checks.

**Alarm fires but Step Functions does not start** — verify the EventBridge alarm rule,
its invocation metric, and that `make control` ran after the alarm existed.

**Global table replica missing** — run
`aws dynamodb describe-table --table-name continuum-state --region ap-southeast-1`.

**Promotion did not serve traffic** — inspect the SSM command output and service state.
Promotion writes `/etc/continuum/role` and restarts the Go systemd service.
