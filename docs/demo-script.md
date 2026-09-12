# Continuum Demo Script (T1–T5)

Before starting, export the API URL and admin token:

```bash
export API_URL=https://<domain>
export ADMIN_TOKEN=<admin-token>
```

Keep CloudWatch, Step Functions, Route 53, and the SNS inbox open. Baseline is
`curl -fsS "$API_URL/health/live"` returning 200 and one successful submission.

## T1 — Hard crash: automatic DNS failover

1. Stop the primary service with SSM:
   `aws ssm send-command --instance-ids "$PRIMARY_INSTANCE_ID" --document-name AWS-RunShellScript --parameters commands='["sudo systemctl stop continuum"]'`.
2. Watch the Route 53 health check flip to Unhealthy and the failover record serve the standby.
3. Observe the Step Functions execution promote the standby, reconcile, and send the completion alert.
4. Submit an attempt against the domain; it should succeed.

Claim demonstrated: downtime is bounded by detection and DNS TTL with no human action.

## T2 — Partial degradation: verified failover

Restore posture, then inject errors and latency through the Go admin API:

```bash
curl -fsS -X POST "$API_URL/admin/chaos" \
  -H "x-admin-token: $ADMIN_TOKEN" -H 'content-type: application/json' \
  -d '{"error_pct":60,"latency_ms":400}'
```

`/health/live` remains a process check while `/health/deep` reports the degraded
application. After the composite alarm fires, observe the verification, promotion,
traffic shift, standby verification, and reconciliation states.

## T3 — DB stall: process alive, platform unavailable

```bash
curl -fsS -X POST "$API_URL/admin/chaos" \
  -H "x-admin-token: $ADMIN_TOKEN" -H 'content-type: application/json' \
  -d '{"db_stall":true}'
```

Pages remain reachable while submissions and deep checks fail. The same verified
failover pipeline should take over.

## T4 — In-flight writes: reconciliation

During a failover, submit a burst with a previously issued student token:

```bash
for i in $(seq 1 30); do
  curl -fsS -X POST "$API_URL/exam/cs101-midterm/submit" \
    -H "Authorization: Bearer $STUDENT_TOKEN" -H 'content-type: application/json' \
    -d "{\"answers\":{},\"request_id\":\"burst-$i\"}" >/dev/null
  sleep 0.3
done
```

Run the Go reconciler Lambda and inspect its summary:

```bash
aws lambda invoke --function-name continuum-reconciler --payload '{}' /tmp/continuum-reconcile.json
cat /tmp/continuum-reconcile.json
```

Rerunning reconciliation is idempotent; repaired submissions appear in
`/admin/submissions`.

## T5 — Transient blip suppression

Restore posture, stop the primary for roughly 40 seconds, then restart it with
SSM. If an execution starts, the later checks should find the service healthy and
end with **Transient blip suppressed — no failover**.

## Reset between scenarios

```bash
curl -fsS -X POST "$API_URL/admin/chaos/reset" -H "x-admin-token: $ADMIN_TOKEN"
curl -fsS -X POST "$API_URL/admin/promote" -H "x-admin-token: $ADMIN_TOKEN"
aws route53 update-health-check \
  --health-check-id "$(terraform -chdir=infra/control output -raw primary_health_check_id)" \
  --no-inverted
```

## Talking points

- DynamoDB Global Tables provide active-standby simplicity and an explicit RPO story.
- Health-check inversion is reversible, auditable, and composes with Route 53 failover.
- The outbox and reconciler make the in-flight write gap measurable and idempotent.
- The Go admin API and Lambda binaries are the only operational control paths.
