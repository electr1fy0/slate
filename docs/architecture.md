# Continuum Architecture

## Overview

```
                          ┌──────────────────────────────────────────────┐
                          │              CONTROL PLANE                    │
                          │  Route 53 (failover records + health checks)  │
                          │  Step Functions: continuum-failover           │
                          │    verify → promote → shift → reconcile       │
                          │    with rollback on verification failure      │
                          │  EventBridge: alarm → StartExecution          │
                          │  SNS alerts · CloudWatch dashboard            │
                          └───────────────┬──────────────────────────────┘
                                          │
        ap-south-1 (PRIMARY)              │             ap-southeast-1 (STANDBY)
┌───────────────────────────────┐         │      ┌───────────────────────────────┐
│ EC2 t3.micro: FastAPI app     │         │      │ EC2 t3.micro: same app        │
│ role=ACTIVE                   │         │      │ role=STANDBY (read-only page) │
│ Verifier Lambda (rate 1 min)  │         │      │ Verifier Lambda (rate 1 min)  │
│   → CW metric Health{primary} │         │      │   → CW metric Health{standby} │
│ Composite alarm (3×60s) ──────┼── EB ───┘      │ Composite alarm (observe only)│
└──────────────┬────────────────┘                └──────────────┬────────────────┘
               │                                                │
               └────────────► DynamoDB Global Table ◄───────────┘
                              async replication (~<1s RPO window)
               S3 assets-pri ──CRR──► S3 assets-stb
```

## Components

### Data plane
- **DynamoDB Global Table** `continuum-state` — single-table design:
  - `EXAM#<id>` / `META`, `QUESTION#nnn` — exam definitions
  - `SUBMISSION#<exam>` / `STUDENT#<id>` — versioned attempts (`request_id` for idempotency)
  - `JOURNAL#<request_id>` / `OUTBOX` — transactional outbox, written in the *same*
    TransactWriteItems call as the submission. This is the reconciliation source of truth.
  - `HEARTBEAT` / `PROBE#...` — TTL'd probes written by the deep health path
- **S3 cross-region replication** for static assets; bundle buckets per region hold the
  deployable app tarball.

### Compute
- One **t3.micro** per region in a minimal VPC (public subnet, no NAT gateway, no ALB —
  cost discipline). Elastic IP per instance. systemd-managed uvicorn.
- The app exposes `/health/live` (process up) and `/health/deep` (DynamoDB roundtrip probe,
  rolling 5xx rate, disk, load). Standby nodes answer `/` with a read-only maintenance page.
- Built-in chaos middleware (`/admin/chaos`) injects error rates, latency, or a "DB stall"
  state where the process is alive but every DB check fails — the case a port check misses.

### Verification layer ("intent-verified")
- A Lambda runs every minute per region. It does two independent probes:
  HTTP deep-check of the app, plus its own direct DynamoDB write/read/delete roundtrip.
- Result → custom metric `Continuum/Health{Role}`. The composite alarm requires **3
  consecutive failures over 3 minutes** (`treat_missing_data = breaching`, so a dead
  verifier also counts as unhealthy).
- Route 53's own health checks (30s interval, threshold 3) run independently against
  `/health/live` as the fast path for hard crashes.

### Orchestration (Step Functions)
Triggered by EventBridge when the primary alarm enters ALARM:

1. **VerifyFailure** — three more force-checks spread over ~2 minutes. Any healthy result
   ends the workflow with a "transient blip suppressed" notification. No failover on flukes.
2. **Notify** — SNS "FAILOVER STARTING".
3. **CheckReplication** — writes/reads a marker item to confirm the local replica is live.
4. **PromoteStandby** — SSM RunCommand flips the standby's role file to `active` and
   restarts the service; it now serves exams.
5. **ForceTrafficShift** — Route 53 `UpdateHealthCheck(Inverted=true)` on the primary's
   health check. Inverting the check makes DNS treat the healthy primary as failing,
   flipping the failover record set to secondary. Fully reversible, auditable via
   CloudTrail, and it composes with (rather than fights) Route 53's native failover.
6. **VerifyStandby** — two force-checks of the promoted node. Failure here triggers
   **RollbackTraffic** (invert back) + alert + failed execution.
7. **ReconcileInFlightWrites** — scans `JOURNAL#` entries from the lookback window,
   compares each against the current item, replays missing versions with a conditional
   put keyed on `request_id` + `version`. Idempotent by construction.
8. **NotifySuccess** with the reconciliation summary.

Hard crash path (EC2 destroyed): Route 53 fails traffic over on its own within ~90s while
the same state machine handles promotion/reconciliation in parallel — DNS never depends on
the orchestrator, and the orchestrator can act without waiting for DNS.

### Consistency model (stated honestly)
Global Table replication is asynchronous. Measured lag is typically <1s, so RPO ≈ lag at
failure instant. Three write classes during an incident:
| Class | Outcome |
|---|---|
| Acked by DDB, replicated before death | Safe |
| Acked by DDB, not yet replicated | Replicates when region returns; reconciler verifies |
| Accepted by app, lost before DDB commit | Recovered from outbox journal by reconciler |

Conflicts use last-writer-wins timestamps; the reconciler's conditional puts prevent
clobbering newer versions.

## IAM least privilege
Each component has a scoped role: instances get their table + their bundle bucket +
namespace-conditioned PutMetricData; verifier gets table heartbeat ops + metrics;
ops gets SendCommand pinned to one instance ARN + UpdateHealthCheck on one health check ARN;
reconciler gets table read/conditional-write + metrics. No wildcards except the
namespace-conditioned metric publish.

## Cost profile
Left running 24/7: ~$16/mo (dominated by 2× t3.micro ≈ $15). With teardown between demo
windows (Makefile `destroy-all`): $2–5/mo effective. DynamoDB on-demand global tables,
Lambdas, Step Functions, schedulers: <$1/mo at project volumes. Route 53 zone ~$0.50 +
domain ~$3/yr. Set a billing alarm at $15 on day one.
