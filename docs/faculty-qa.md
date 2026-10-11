# Continuum: faculty questions

**Ayush · 24BIT0394 · BCSE355L**

## What is the problem?

A timed assessment can recover its web server while leaving the student unsure whether the final answer committed. Continuum couples application recovery with a durable, replayable submission receipt. The original server deadline continues during recovery.

## What does “intent verified” mean?

The intent is an explicit policy: the active application must be able to use its database, the standby must be ready, and a promoted route must pass a public health check. It is a versioned policy implemented in code, not a claim of AI or natural-language interpretation.

## Does AWS already provide failover?

Yes. AWS provides health checks, routing, durable storage, and orchestration. Our contribution is the assessment contract across these building blocks: confirmation of sustained failure, conditional writer promotion, transactional fencing, exact receipt replay, and idempotent audit work. AWS could host another implementation of that contract; the project is not claiming that AWS lacks custom health checks.

## Why use a custom router?

For a small faculty demonstration, an HTTP API and Lambda router avoid an always-on load balancer and a purchased domain. The router reads an authoritative backend/epoch record on each request. The trade-offs are request charges, latency, bounded payloads/timeouts, a shared control record, and responsibility for routing correctness. This is not a general replacement for an AWS load balancer.

## Why two EC2 instances rather than only Lambda?

Separate EC2 instances make the target application's failure concrete: the demo can stop an actual application host and recover through another host in a different Availability Zone. Lambda runs the bounded routing, verification and orchestration work. Both EC2 instances are stopped between cloud rehearsals.

## Why is the database shared?

A regional DynamoDB table keeps durable assessment data available to either application instance. Promotion changes the authorised writer rather than copying or promoting a database. An acknowledged submission stays in that shared durable store. A DynamoDB or complete Region outage remains outside this recovery boundary.

## What is reconciled if there is no replica?

The final submission transaction commits its receipt and an outbox entry with the canonical answer. Reconciliation completes pending downstream audit work. It does not reconstruct an answer that never committed, recover lost regional replicas, or turn a browser buffer into a server acknowledgment.

## How does an old backend get blocked?

The router supplies the active backend and a monotonically increasing epoch. Critical mutations check both values inside their write transaction. After promotion, the former writer fails that condition. The recorded AWS proof rejects a stale registration and confirms that no email index was created. Separate exam regressions verify that stale operations cannot change saved answers/version or create a final submission, receipt or outbox entry.

## Is this “exactly once”?

The demonstrated guarantee is one logical final submission per accepted request, with payload binding and idempotent downstream audit work. A client can send several HTTP attempts. Exact repeats recover the same stored receipt; different answers under the same request ID return a conflict. The guarantee depends on the regional durable store and transaction path remaining available.

## What happened in the actual AWS trial?

The active EC2 instance was stopped. B became active at epoch 2. The externally observed interval from fault action through public health and completed reconciliation was **50.472 seconds**. The narrower monitor-check-cycle-to-public-verification interval was **23.713 seconds**. Schedule delay is outside the narrower interval. These are observations from one bounded trial, not an availability SLA.

## What if the standby is also unhealthy?

The controller refuses promotion. It leaves the route/epoch unchanged and records the suppressed decision. A short fault that recovers before the confirmation window is also suppressed. Restoring service safely is preferable to declaring a successful switch to another broken application.

## Can an answer be submitted after time expires?

A new, uncommitted late submission can be rejected. An exact replay of a previously committed request can recover that existing receipt after the deadline. A browser draft alone does not prove the server accepted it.

## What can the local rehearsal prove?

The local launcher runs two HTTP servers inside one Go process with shared memory. Closing one listener exercises routing decisions, receipt behavior, and fencing. It does not prove resilience to loss of the entire process, laptop, Availability Zone, or Region. The saved cloud trial separately stops an actual EC2 instance.

## How is spending controlled?

Develop and rehearse locally. Resume AWS only for a bounded trial, inspect credits in AWS Settings → Billing, then pause both EC2 instances and the monitor schedule. Keep the Free plan. Retained disks, buckets and table storage can still consume credits while compute is stopped. Destroy the demo after exporting the evidence when it is no longer needed.

## What is outside the project?

Complete regional disaster recovery, production examination identity, proctoring, anti-cheating, university-wide capacity, and collaborative WebSocket recovery are outside the demonstrated workload. The assessed product is a timed HTTP examination application with application-instance recovery and receipt consistency.

Evidence and operation details: [implementation review](implementation-review.md), [current runbook](demo-runbook.md), [cloud rehearsal](evidence/cloud-rehearsal.json), and [stale-writer proof](evidence/cloud-stale-writer.json).
