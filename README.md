# Continuum

Verified recovery for timed academic assessments. The demo combines a student exam, a recovery cockpit, transactional writer fencing, and replayable submission receipts.

## Run the local demo

```sh
cd web && bun install --frozen-lockfile && cd ..
make demo-local
```

Open **http://localhost:3000**. Read the private `.demo/local.env` file for the faculty login and cockpit token. Register a synthetic student in a separate browser session and select **Cloud Architecture CAT**. The cockpit is at `/ops`.

Local mode runs separate A/B HTTP servers with shared memory. Cloud mode runs two EC2 instances in separate Sydney Availability Zones with shared regional DynamoDB. The local mode is labeled honestly and is useful for rehearsals without using AWS credits.

## What to demonstrate

- Stop the active backend and observe verified promotion, a newer epoch, and successful public routing.
- Keep liveness healthy while disabling that backend's database capability; recover only when the standby is ready.
- Lose a response after a submission commits, then recover the same receipt using the original request ID and answer snapshot.
- Refuse an unsafe standby and suppress a brief failure.
- Repeat audit reconciliation without duplicate downstream work.

The original server deadline continues across failover. A browser buffer is not a durable acknowledgment. Complete Region or DynamoDB outages are outside the demonstrated recovery boundary. Classroom boards/WebSockets are outside this bounded HTTP assessment demo.

## AWS deployment

Keep the project on its **Free** plan and all regional resources in **ap-southeast-2**. The deployable stack is `infra/demo`; it uses no NAT gateway, always-on load balancer, purchased domain, cross-Region resources, or database replicas.

```sh
make demo-deploy
make demo-resume  # enable monitoring for a rehearsal
make demo-pause   # stop EC2 and disable the schedule afterwards
```

Terraform is required for those commands. This workspace also has a verified local binary at `.tools/terraform`; use `make TF="$PWD/.tools/terraform" demo-deploy` if Terraform is not on your PATH. Cloud resources use credits while active, and retained disks/storage can use credits while paused. The project currently has a small Lambda concurrency quota; the stack is sized for a faculty demonstration rather than production load.

Private cloud URLs and credentials are written to `.demo/aws-output.json`. Keep it and Terraform state out of Git and projected screenshots.

## Checks

```sh
go test -race ./...
go vet ./...
make web-check
bash scripts/build_demo.sh
terraform -chdir=infra/demo init -backend=false
terraform -chdir=infra/demo validate
```

The real-HTTP integration test covers an uncertain submission, backend recovery, replay after the exam closes, unchanged request IDs, one canonical submission/audit, and monotonic failback. Additional tests verify transaction conditions, incident leases, unsafe-promotion suppression, rollback decisions, and frontend reload/retry behavior.

## Project materials

- [Faculty presentation — editable PowerPoint](docs/presentation/continuum-faculty-pitch.pptx)
- [Faculty presentation — PDF](docs/presentation/continuum-faculty-pitch.pdf)
- [Faculty questions and concise answers](docs/faculty-qa.md)
- [Faculty pitch and revised proposal](docs/faculty-pitch.md)
- [Deployment, cost controls, and demonstration runbook](docs/demo-runbook.md)
- [Implementation/review evidence](docs/implementation-review.md)
- [Original free-plan design study](docs/continuum-free-tier-plan.md)

The previous cross-Region proposal remains under `infra/core`, `infra/standby`, and `infra/control` for historical reference. Its apply/destroy Make targets are disabled because that architecture is unsupported by this AWS project experience. The supported implementation is `infra/demo` and `statemachine/demo-failover.asl.json`.
