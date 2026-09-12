# Continuum

An intent-verified automated failover framework for mission-critical academic platforms on AWS.

A self-healing, cross-region infrastructure layer that detects degradation of a primary
deployment, verifies it at the application level (not just ping), fails over to a standby
region through an orchestrated Step Functions workflow with rollback, and reconciles writes
that were in flight during the transition.

Demo target application: an online exam-delivery platform.

## Architecture in one paragraph

Two regions (default `ap-south-1` Mumbai primary, `ap-southeast-1` Singapore standby) each
run the Go exam API on EC2, backed by one DynamoDB Global Table and S3 cross-region
replication. Route 53 failover records handle hard crashes automatically. A scheduled
Lambda verifier performs deep health checks (DB roundtrip, error rate, disk) and feeds a
composite CloudWatch alarm; sustained degradation triggers an Step Functions state machine
that re-verifies, promotes the standby out of read-only mode, forces the DNS shift,
verifies the new primary, rolls back if verification fails, and finally replays any
in-flight writes found in the transactional outbox.

## Repository layout

```
cmd/api/              Go exam API entrypoint
cmd/*/                Go Lambda entrypoints (verifier, failover ops, reconciler)
internal/             Go domain, DynamoDB, auth, HTTP, WebSocket, health, and control-plane packages
web/                  Slate — TanStack Start frontend (classroom boards, quizzes, ops console)
statemachine/         Step Functions ASL definition
infra/
  modules/            network, compute (reusable)
  core/               ap-south-1 stack + global data plane (DynamoDB GT, S3 CRR)
  standby/            ap-southeast-1 stack (network + compute only)
  control/            Route 53, Step Functions, ops/reconciler Lambdas, dashboards
scripts/              App bundling/deployment helpers (+ local dev stack)
deploy/               systemd and instance lifecycle assets
scripts/              Go build, deployment, and local development helpers
docs/                 architecture, runbook, demo script
```

## Prerequisites

- AWS account with admin credentials configured (`aws configure` or env vars), MFA on root.
- Terraform >= 1.6 (`brew install hashicorp/tap/hashicorp`)
- Go 1.27+
- A Route 53 domain (optional but strongly recommended — see below)

## Deploy order

```bash
make core        # primary region + DynamoDB Global Table + S3 CRR
make standby     # standby region compute
make app-deploy  # bundle app -> S3 -> EC2 both regions
make control     # Route 53 + Step Functions + orchestrator lambdas
```

Copy each `terraform.tfvars.example` to `terraform.tfvars` first.

## Validation tests

Automated suite:

```bash
go test -race ./...
make test          # Go unit and integration suite
```

The Go suite covers the API, WebSocket protocol, DynamoDB persistence, health
verification, failover operations, reconciliation, and chaos middleware. CI also
builds the Linux API bundle and all Lambda artifacts.

See `docs/demo-script.md` for the live T1-T5 demo scenarios.

## Frontend development (Slate)

`web/` is a TanStack Start app (React 19, Tailwind v4, shadcn/ui base-nova
theme) with three surfaces: live Excalidraw classroom boards synced over
WebSockets (`/board/{room}`), quiz authoring + submissions (`/teacher`), and a
Continuum ops console with chaos injection (`/ops`). Students take quizzes at
`/quiz/{examId}`; submissions are graded server-side on submit.

```bash
make web-install    # bun install
scripts/dev_go_api.sh # terminal 1: Go API on :8000 (set DDB_ENDPOINT_MODE for local DynamoDB)
make web-dev        # terminal 2: Vite dev server on :3000, /api + /ws proxied
make web-check      # typecheck + eslint + production build
```

Notes:
- Boards relay through `internal/boards` (in-memory rooms); snapshots autosave to the
  browser's localStorage per room. Board state is ephemeral by design — it does
  not survive a full-cluster restart.
- Quiz authoring and chaos endpoints require the admin token (default
  `continuum-demo-token`, override with `ADMIN_TOKEN`).
- The student "draw" permission on a board is teacher-side and client-enforced;
  fine for classrooms, not adversarial audiences.

## Cost discipline

Run `make destroy-all` after demo windows. Expected steady cost if left running ~$16/mo;
with teardown between sessions ~$2-5/mo. Set a $15 billing alarm in the console on day one.
