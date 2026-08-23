# Continuum Demo Script (T1–T5)

Open before starting:
- Terminal with `python3 chaos/continuum_chaos.py` ready
- Browser tabs: CloudWatch dashboard "continuum-overview", Step Functions executions page,
  Route 53 health checks page, inbox for SNS emails

Baseline: both nodes healthy, `status` shows green, one submission works.

---

## T1 — Hard crash: automatic DNS failover (Route 53 fast path)

1. `python3 continuum_chaos.py hard-down --target primary`
2. Watch Route 53 health check `continuum-primary-hc` flip to Unhealthy (~90s).
3. DNS failover record now serves standby EIP. With a domain: `dig +short <domain>`
   shows the Singapore IP. Without a domain: show the record sets in the console.
4. Meanwhile the alarm breaches → Step Functions execution runs → standby promoted,
   reconciliation, "FAILOVER COMPLETE" email.
5. Submit an attempt against the domain/standby — it succeeds.

**Claim demonstrated:** downtime bounded by detection + TTL, zero human action.

## T2 — Partial degradation: orchestrated verified failover

Restore posture first (`heal`, demote standby, promote primary, un-invert HC).

1. `python3 continuum_chaos.py degrade --error-pct 60 --latency-ms 400`
2. `/health/live` still returns 200 — a ping-style check stays GREEN. Show this.
3. `/health/deep` fails on error-rate. Verifier metric drops to 0.
4. After ~3 min the composite alarm fires → Step Functions graph walks through
   VerifyCheck1→3 (all unhealthy) → promote → shift → verify standby → reconcile.
5. Show the execution visual + the "FAILOVER STARTING"/"COMPLETE" emails.

**Claim demonstrated:** application-aware verification catches degradation that
infrastructure-level checks miss; failover is an auditable workflow, not a reflex.

## T3 — DB stall: process alive, platform dead

1. `heal`, restore roles, then `db-stall on`
2. Pages serve fine (process up), but every exam submit fails and deep checks report
   ddb probe failure while port checks stay green.
3. Same pipeline as T2 takes over.

**Claim demonstrated:** the verifier inspects real system state, not liveness.

## T4 — In-flight writes: reconciliation closes the gap

During a T2/T3 failover window, hammer submissions:

```bash
for i in $(seq 1 30); do python3 continuum_chaos.py submit --student burst$i; sleep 0.3; done
```

Some land before death (replicated), some are accepted-then-lost at commit time.
After failover completes:

```bash
python3 continuum_chaos.py reconcile
```

Output shows scanned / repaired / already_replicated / conflicts; repaired submissions
appear in `/admin/submissions`. Idempotency: rerun reconcile → all already_ok, no
duplicates.

**Claim demonstrated:** RPO gap handled by design, not luck; replays are idempotent.

## T5 — Transient blip suppression (negative test)

1. Restore posture. Then: `hard-down --target primary --seconds 40`
2. Instance returns after ~40s+boot. The alarm may breach briefly or not at all;
   if an execution starts, Check2/Check3 find the app healthy again → workflow ends
   with **"Transient blip suppressed - no failover."**

**Claim demonstrated:** the system does not flap. Verified intent gates every shift.

---

## Reset between scenarios

```bash
heal && demote --target standby && promote --target primary
aws route53 update-health-check --health-check-id $(terraform -chdir=infra/control output -raw primary_health_check_id) --no-inverted
```

## Talking points for evaluation

- Why DynamoDB Global Tables instead of RDS promotion? Cost, active-standby simplicity,
  and honest RPO story vs irreversible replica promotion.
- Why invert the health check instead of ChangeResourceRecordSets? Reversible single API
  call, keeps Route 53 native failover as the safety net, full CloudTrail auditability.
- What's the actual RPO? Measured replication lag (<1s typical) + outbox replay for the
  app-to-DDB window. Show the reconciler numbers live in T4.
- Well-Architected mapping: reliability (this whole demo), cost optimization (teardown
  discipline, no NAT/ALB), operational excellence (IaC, runbook, chaos CLI), security
  (least privilege per component, KMS-by-default services), performance (single-table
  access patterns).
