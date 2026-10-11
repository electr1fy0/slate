# Continuum: Intent-Verified Recovery for Timed Academic Assessments

**BCSE355L · Cloud Architecture Design · Fall 2026–2027**  
**Ayush · 24BIT0394**

## The pitch

An examination platform can return to “healthy” while students still cannot submit answers, or while they cannot tell whether their last submission was accepted. Continuum makes recovery depend on the operation the student needs: accept an answer safely and return evidence of its committed submission.

The demonstration combines a timed assessment application with an observable recovery controller. Two application instances run in separate Availability Zones in one AWS Region. A custom router selects the active instance from an authoritative control record. An outside-VPC monitor confirms sustained, active-specific failure with three spaced readiness probes before starting Step Functions. The workflow acquires an incident lease, rechecks active and standby readiness under that lease, advances the writer epoch, verifies the public route, and completes pending audit work. The submission transaction checks that its writer still owns the current epoch, preventing an old instance from committing an assessment write after promotion.

Each final submission carries one persistent request ID and an immutable answer snapshot. If the server commits it but its response is lost, a retry returns the same durable receipt—even after the exam window closes. A browser buffer is shown separately from a confirmed server receipt. The controller also refuses to promote an unhealthy standby and suppresses short failures that recover before the confirmation window.

## A 45-second opening

> “Continuum protects the last minutes of an online exam. I’ll stop the active application while a student is answering, show the recovery decision, and submit through the recovered instance. Then I’ll lose a response after the answer has already committed. The student retries the same request and gets the existing receipt, with one submission and one audit summary. The project demonstrates safe recovery of an academic operation, as well as recovery of its web server.”

## What is differentiated

| Demonstrated problem | Continuum mechanism | Evidence to show |
|---|---|---|
| An alive process cannot perform exam writes | Deep database readiness plus repeated confirmation | Liveness stays green while readiness turns red |
| An old writer finishes after a route switch | Backend ID and monotonic epoch checked inside the exam write transaction | AWS rejected a stale-epoch registration with HTTP 503; exam-write fencing is separately covered by transaction/API tests |
| A commit succeeds but its response disappears | Persistent request ID, payload binding, durable receipt | The [rehearsal JSON](evidence/cloud-rehearsal.json) records the same request ID and commit timestamp after retry |
| A brief fault or bad standby triggers an unsafe switch | Confirmation window, standby recheck, incident lease | Suppressed promotion with unchanged epoch |
| Downstream work is interrupted after a commit | Atomic submission outbox and idempotent audit reconciliation | Exactly one summary after repeated reconciliation |
| A small project pays for idle infrastructure | Bounded custom routing and scheduled verification; pause between rehearsals | Deployment resource list and credit ledger |

AWS supplies strong building blocks, including transactional DynamoDB writes and configurable application health checks. Continuum adds the application contract across those blocks. Its custom router is a prototype trade-off: it avoids an always-on load balancer and purchased DNS infrastructure, while adding Lambda/API Gateway latency, request charges, payload limits, and implementation responsibility. It is not a general replacement for an AWS load balancer.

## Eight-minute faculty demonstration

1. **Baseline (one minute).** Open the student exam and the administrator cockpit in separate browser sessions. Show A active, B ready, and the initial epoch. Explain that an answer buffered on the device is different from an answer acknowledged by the server.
2. **Application failure (two minutes).** In AWS mode, click **Stop EC2 instance**. Continue selecting answers. Show consecutive failed probes, B promotion, a newer epoch, and public-route verification. Submit before the original deadline and show the receipt. In local mode, click **Stop local HTTP server** and explain that it closes that server inside a shared Go process.
3. **A misleading green health check (one minute).** Reset and verify the baseline. Inject the active backend's capability failure. Show healthy liveness, failed database readiness, and verified recovery. This is an application-local injected DB-access failure; do not describe it as a DynamoDB service outage.
4. **The ambiguous submission (two minutes).** Use a fresh student/attempt. Arm **Drop receipt**, then submit. The current local result screen shows the receipt reference and submitted time. The deployed cloud frontend predates that reference display, so use the [rehearsal JSON](evidence/cloud-rehearsal.json) to compare the original/replayed request ID and commit timestamp until the frontend is updated. Verify one canonical submission and one audit summary; rerun reconciliation to show that it adds no duplicate work.
5. **Refusing an unsafe decision (one minute).** Make the standby unsafe and fail the active application's capability. Show that the route and epoch stay unchanged while both are unready. Reset and verify before ending.
6. **Cost and scope (one minute).** Show the single-Region architecture, absence of NAT/load balancer/replicas, and the pause command. Explain that a shared DynamoDB or Region outage remains outside the demonstrated recovery boundary.

## Honest claims and measurements

The recorded Sydney rehearsal on 10 October 2026 stopped the actual active EC2 instance and observed B at epoch 2 with public health and completed reconciliation after **50.472 seconds**. The controller measured **23.713 seconds** from the start of its monitor check cycle to public verification, including the initial probe. Exact replay returned the same request ID and commit timestamp; independent DynamoDB inspection found one canonical submission, one receipt, one processed journal, and one audit summary. Repeated reconciliation processed zero further records. A short fault and an alive-but-unready standby were both suppressed. See [rehearsal evidence](evidence/cloud-rehearsal.json) and [AWS record/workflow inspection](evidence/cloud-records.json). These are results from one bounded rehearsal, not an SLA.

- The recovery target is an application-instance failure while the regional DynamoDB store, router, and control plane remain available. AWS placement spans two Availability Zones. The local rehearsal uses two HTTP servers in one Go process and demonstrates routing decisions, fencing, and receipt behavior; it cannot establish AWS infrastructure resilience.
- An acknowledged final submission has a committed canonical record and receipt. The tested failure should lose **zero acknowledged submissions** under that dependency assumption. Buffered answers that have not committed do not have that guarantee.
- Measure recovery from fault injection to successful public-route verification, and also record the monitoring delay. A one-minute AWS schedule adds up to roughly one minute before the confirmation window. Do not advertise near-zero recovery time or an availability SLA from a classroom test.
- The original server deadline continues across recovery. A new, uncommitted submission after the allowed deadline can be rejected; an exact replay of an already committed request can recover its existing receipt.
- The custom route supports bounded HTTP exam operations. Collaborative WebSockets, complete regional disaster recovery, proctoring, anti-cheating, production identity management, and recovery of every classroom feature are outside this assessment demo.
- “Intent verified” means a versioned, explicit application recovery policy. It does not imply an AI model or natural-language intent analysis.

## Revised proposal abstract

Continuum is an application-aware recovery framework for timed academic assessments on AWS. The prototype runs two application instances in separate Availability Zones within the project's selected Region and stores assessment state in a shared regional DynamoDB table. A verifier distinguishes process liveness from database-backed application readiness, and a Step Functions workflow coordinates incident locking, standby verification, conditional routing, public-route smoke testing, safe rollback, and idempotent audit reconciliation. A monotonic writer epoch is checked within each critical assessment write transaction, while persistent submission request IDs enable recovery of an existing receipt after a lost response. Fault injection demonstrates application-instance failure, partial capability loss, ambiguous submission outcomes, and suppressed unsafe promotion. The design is evaluated using observed recovery duration, receipt consistency, stale-write rejection, and bounded credit usage. Its scope is application recovery with the regional data and control services available, rather than complete regional disaster recovery.
