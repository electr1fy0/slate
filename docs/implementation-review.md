# Continuum implementation and review evidence

The implementation uses three Luna agents at maximum reasoning effort for the backend, recovery controller, and frontend. The primary agent reviewed their changes, the deployment wiring, the recovery guarantees, and the faculty pitch.

## Review corrections

- Persistent submission IDs bind to an immutable answer snapshot. Retries preserve that ID, including after a page reload, and reject a changed payload under the same ID. Transient throttling is retryable; final client errors do not create an endless retry loop.
- Client retries span the scheduled cloud recovery window. A buffer on the device is labeled separately from a confirmed server receipt.
- Submission, receipt, attempt completion, and pending audit work commit in one DynamoDB transaction with the writer epoch condition. Exact receipt lookup precedes deadline rejection, so replay can recover a previously committed submission after the exam closes.
- Audit reconciliation verifies the canonical submission, creates one answer-free summary, and marks the journal processed atomically. It carries a scan continuation across bounded invocations. A matching pre-existing audit repairs a pending journal only after atomic checks of the canonical record and the entire target key.
- The transient-fault duration expires before the configured confirmation window. Delayed cold starts can miss that short fault; they cannot justify extending it into a sustained outage.
- Registration, administrative user creation, teacher edits, and other durable mutations also carry the writer condition. Only protected ephemeral chaos controls bypass routing headers. Background board snapshots retain their originating writer instead of borrowing a newer epoch during flush.
- Incident ownership and route changes are retry-safe. Standby health is checked again under the incident lease. Rollback requires fresh health of the former backend. A failed public smoke test cannot end as a successful recovery.
- The ASL definition uses supported Step Functions intrinsics. Reconciliation conflicts produce a failed workflow rather than a false success.
- Manual cloud reconciliation starts a separate asynchronous workflow, keeping backlog processing outside the public API timeout. Required completion-event storage errors fail the task instead of returning a false recorded result.
- Cloud deployment respects the project's Sydney-only restriction and Free plan. It has no reserved Lambda concurrency, NAT gateway, purchased domain, cross-Region replica, or load balancer.
- Instance bootstrap preserves existing exam windows/questions. Credentials remain in ignored private files, service environment configuration, and an encrypted Systems Manager parameter.
- Fresh demo exams have a server-enforced 15-minute duration and one attempt. The existing cloud seed was given that duration through a conditional transaction while compute was stopped; bootstrap still preserves faculty edits. Timer/deadline enforcement uses the same persisted attempt across backend changes.
- Pause/resume persist their state so a subsequent frontend deployment does not silently restart EC2. Deployment archives are reproducible and the app bundle hash triggers instance replacement when backend code changes.
- The live rehearsal exposed a persisted event mapping bug: `duration_ms` was written correctly but read as zero. Explicit DynamoDB tags and a write/query round-trip regression now preserve that metric. Cloud duration includes the initial monitor probe and excludes earlier schedule delay; local duration begins with its first failed probe.
- A Terraform refresh after the live instance stop reported the IPv4 launch-association setting as false and proposed replacing that stopped node. The launch setting now uses a narrowly scoped lifecycle ignore; new instances still request it. User-data/bundle changes continue to trigger deliberate replacement. This follows Terraform's [creation versus update lifecycle behavior](https://developer.hashicorp.com/terraform/language/meta-arguments/lifecycle#ignore_changes).

## Verification

| Check | Result |
|---|---|
| `go test -p 2 -race ./...` | All packages passed, including actual HTTP recovery/replay, board background-write fencing, and event persistence |
| Final timed-seed configuration | Full HTTP API suite passed; focused seed/receipt race tests and full integration/controller race tests passed afterwards |
| `go vet ./...` | Passed |
| Web typecheck and lint | Passed |
| Full web unit/component suite | 108 tests across 16 files passed; one worker and 15-second test timeout while concurrent builds were running |
| Final cockpit text regression | 3 tests passed after the metric explanation update |
| Production frontend and Linux deployment artifacts | Built successfully with the actual HTTPS API origin |
| Terraform formatting and validation | Passed |
| Step Functions definition validation | AWS returned `OK`, with zero diagnostics |
| Shell syntax, Python compilation, Git whitespace checks | Passed |
| Deployment archive reproducibility | All six archives reproduced byte-for-byte; Lambda bootstrap executable mode preserved |

Long-running race instrumentation initially exposed a fixture timing problem: full-strength password hashing under simultaneous test/build load exceeded the router's real eight-second timeout. The recovery fixtures now seed a synthetic test student with minimum-cost hashing and then use real HTTP login; separate authentication and registration tests retain their coverage. Production password hashing and router timeouts were not relaxed.

## Live AWS rehearsal

On 10 October 2026, the deployed API passed an automated rehearsal that stopped the actual active EC2 instance. The regional database and control plane remained available.

- A was active at epoch 1. A 7.5-second fault recovered before the confirmation window and left that epoch unchanged.
- Injected application-local database stalls preserved both processes' liveness while failing readiness. The controller recorded suppression and refused to promote an unsafe standby.
- A synthetic submission committed before its response was dropped with HTTP 503. The rehearsal then stopped A through the authenticated control endpoint and actual EC2 StopInstances operation.
- The scheduled controller promoted B to epoch 2, verified the public deep-health route, and completed audit reconciliation. External observation took **50,472 ms** from the start of the fault action through observed recovery, reconciliation, and a final public-health request. This includes scheduling and polling overhead. The controller's narrower check-cycle-to-public-smoke duration was **23,713 ms**, including its initial probe.
- An exact replay returned the same request ID and **unchanged commit timestamp**; the request/response comparison is recorded in the [rehearsal JSON](evidence/cloud-rehearsal.json). Altered answers under the same ID returned HTTP 409. The dashboard reference view is not presented as this comparison evidence.
- Independent AWS API inspection found exactly one canonical submission, one receipt, one processed outbox record, and one audit summary for that request. The automatic workflow succeeded with `processed=1` and `conflict_count=0`; a separate reconciliation workflow succeeded with `processed=0` and `conflict_count=0`.
- After A restarted, an AWS Systems Manager command sent a synthetic registration directly to A with the old epoch 1. It returned HTTP 503 with `WRITER_FENCED`; a strong read of the actual `USEREMAIL#… / LOGIN` key found no created user index. B remained active at epoch 2. This [live stale-registration proof](evidence/cloud-stale-writer.json) supplements separate transaction/API tests for exam submission fencing. The rehearsal did not send a stale exam submission to AWS.

The original [rehearsal JSON](evidence/cloud-rehearsal.json) is preserved unchanged, including the zero metric returned before the serialization correction. [Independent AWS inspection](evidence/cloud-records.json) contains the correctly stored workflow duration and successful state sequence. An [EC2 state inspection](evidence/cloud-instance-fault.json) confirms A stopped while B ran in the other Sydney AZ. The [post-review status](evidence/cloud-status-after-review.json) and final dashboard screenshot record the corrected metric readback. The application bundle from the successful live run was retained during this telemetry update; rebuilding the shared Go package for a later full app deployment deliberately replaces both nodes.

The public frontend is [the deployed CloudFront shell](https://d2tshu1xfmrfz.cloudfront.net). AWS compute is paused after evidence capture; run the documented resume command and verify both backends before another cloud rehearsal. The local launcher provides a rehearsal without using AWS credits.

The currently deployed control Lambda predates the runtime-gated Reset guard added in this repository. Until the next control-Lambda deployment, the paused AWS stack does not have this application-level protection, so do not use cockpit **Reset** while that deployment is paused; keep it paused or resume through the documented `make demo-resume` lifecycle. After deployment, Terraform enables Reset only when both `instances_running` and `monitor_enabled` are true, and the handler rejects Reset before making EC2 calls otherwise. No deployment or AWS mutation was attempted for this documentation/code correction because the AWS connector was unavailable.

Final AWS inspection confirmed **both EC2 instances stopped** and the monitor rule **DISABLED**, with the timed exam configuration preserved. See [paused-state evidence](evidence/aws-paused.json). The last plan API check reported the Free plan active, $140 in credits, and expiry on 7 March 2027. Retained disks and storage can still consume credits; no plan upgrade was made.

A full refreshed Terraform plan after pausing returned **No changes**, confirming that stopped instances and the disabled monitor stay paused. A real local student request produced a deadline exactly 900,000 ms after its persisted start; the student UI shows the countdown and saved-answer status. See [timer evidence](evidence/local-timed-exam.json).

Visual evidence: [AWS recovery cockpit](evidence/cloud-recovery.png), [timed student assessment](evidence/student-timed-exam.png), and [completed synthetic receipt](evidence/student-receipt.png). The student views are a local rehearsal; the AWS instance-stop proof is recorded separately above.

## Faculty readiness pass — 11 October 2026

The student result now displays its exact receipt reference, and the cockpit labels the narrow timing metric **Probe-to-verify**. These frontend changes are available in the local rehearsal and a successful production build; they have not been published to the paused AWS frontend in this pass.

The stale-exam regression first persists an answer/version under A, promotes the test guard to B, and verifies that stale A cannot change that answer/version or create a submission, receipt or outbox entry. B can subsequently save and submit. The focused store race test passed. The new runtime-gated Reset guard passed the complete controller package race checks (`go test -race ./internal/continuum`, 2.081 seconds); focused frontend tests passed 31 tests, with typechecking and lint clean. Terraform format and validation passed.

A real local UI rehearsal closed A's HTTP listener, promoted B at epoch 2, completed reconciliation, and recorded 10,017 ms from the first failed probe to public verification. Reloading the previously completed student's result through B displayed the same receipt reference and submitted time. Reset restored both backends and safely failed back. This local result is separate from the recorded AWS EC2-stop experiment. See the [reviewed local cockpit](evidence/local-recovery-reviewed.png) and [student receipt reference](evidence/student-receipt-reference.png).

The faculty package includes an editable [nine-slide presentation](presentation/continuum-faculty-pitch.pptx), its [PDF](presentation/continuum-faculty-pitch.pdf), presenter notes, and [faculty questions](faculty-qa.md). Each slide and PDF page was visually inspected. Historical architecture and operational documents are archived with explicit notices, while their original entry points direct readers to current instructions.

## Demonstration boundary

The supported cloud demonstration is a small HTTP examination application with two EC2 instances in different Sydney Availability Zones and a shared regional DynamoDB table. Regional data, routing, and orchestration services must remain available. Local rehearsal uses two HTTP servers inside one Go process and shared memory. Its stop action closes the selected HTTP server and connections. AWS board WebSockets are not exposed by this HTTP API.

A successful deep-health smoke confirms the public route. Durable submission correctness is evidenced separately by the canonical transaction and the same request ID/commit timestamp on replay. This is a measured prototype, not a production availability SLA.
