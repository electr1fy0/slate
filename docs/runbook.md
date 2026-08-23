# Continuum Runbook

## First-time setup

1. Configure AWS credentials (`aws configure` or environment). Confirm identity:
   `aws sts get-caller-identity`.
2. Set a billing alarm at $15 (AWS Console → Billing → Budgets) before anything else.
3. Install Terraform: `brew install hashicorp/tap/hashicorp`
4. Copy tfvars examples and edit:
   ```bash
   cp infra/core/terraform.tfvars.example infra/core/terraform.tfvars
   cp infra/standby/terraform.tfvars.example infra/standby/terraform.tfvars
   cp infra/control/terraform.tfvars.example infra/control/terraform.tfvars
   ```
   Set `admin_token` (same value in core + standby) and `alert_email`. If you own a
   domain, set `domain_name` in control; then delegate the nameservers printed by its
   output to your registrar.

## Deploy

```bash
make core        # ~5 min: VPC, EC2 primary, DynamoDB GT, S3, verifier, alarm
make standby     # ~5 min: VPC, EC2 standby, verifier
make app-deploy  # bundle app -> both buckets -> SSM deploy to both instances
make control     # Route53 HCs/records, Step Functions, ops lambdas, dashboard
```

Wait for user-data bootstrap (~3–4 min after instance running), then:

```bash
python3 chaos/continuum_chaos.py status      # both nodes healthy?
python3 chaos/continuum_chaos.py verify      # force deep check via lambda
python3 chaos/continuum_chaos.py submit --student test001   # end-to-end write
```

Confirm the scheduled verifiers are publishing: CloudWatch → Metrics → Continuum namespace
→ Health{Role=primary|standby} should be 1s.

## Redeploying the app after code changes

```bash
make app-deploy
```

## Teardown between demo windows

```bash
make destroy-all    # order: control -> standby -> core (enforced)
```

DynamoDB data is lost on destroy (fine for demos). Buckets are force_destroy.

## Manual operations

| Action | Command |
|---|---|
| Promote standby manually | `python3 chaos/continuum_chaos.py promote --target standby` |
| Demote a node | `python3 chaos/continuum_chaos.py demote --target primary` |
| Inject 30% errors | `python3 chaos/continuum_chaos.py degrade --error-pct 30` |
| DB stall (process alive) | `python3 chaos/continuum_chaos.py db-stall on` |
| 30s blip (should NOT fail over) | `python3 chaos/continuum_chaos.py hard-down --seconds 40` |
| Run reconciler now | `python3 chaos/continuum_chaos.py reconcile` |

After any failover test, restore normal posture:

```bash
python3 chaos/continuum_chaos.py heal
python3 chaos/continuum_chaos.py demote --target standby
python3 chaos/continuum_chaos.py promote --target primary
# if Route 53 health check was left inverted by a manual experiment:
aws route53 update-health-check --health-check-id <PRIMARY_HC_ID> --no-inverted
```

Get the HC id from `terraform -chdir=infra/control output primary_health_check_id`.

## Troubleshooting

**App not reachable on :80** — `aws ssm send-command` a `journalctl -u continuum -n 50`
or SSH in. Most common cause: instance booted before `app-deploy` ran; rerun
`make app-deploy`.

**Verifier metric absent** — check `/aws/lambda/continuum-verifier-primary` logs; usually
IAM or security group. SG must allow :80 from 0.0.0.0/0 (health checks come from AWS IPs).

**Alarm fires but SFN doesn't start** — EventBridge rule pattern matches on the alarm ARN;
confirm `terraform -chdir=infra/control apply` ran *after* the alarm existed, and check the
rule's metrics (Invocations).

**Global table replica missing** — `aws dynamodb describe-table --table-name
continuum-state --region ap-southeast-1`; replicas take a few minutes to become ACTIVE.

**Promotion didn't serve traffic** — SSM command output shows role file + service state.
The app reads `/etc/continuum/role` only at boot; promotion always restarts the service.
