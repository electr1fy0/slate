# Continuum

An intent-verified automated failover framework for mission-critical academic platforms on AWS.

A self-healing, cross-region infrastructure layer that detects degradation of a primary
deployment, verifies it at the application level (not just ping), fails over to a standby
region through an orchestrated Step Functions workflow with rollback, and reconciles writes
that were in flight during the transition.

Demo target application: an online exam-delivery platform.

## Architecture in one paragraph

Two regions (default `ap-south-1` Mumbai primary, `ap-southeast-1` Singapore standby) each
run the FastAPI exam app on EC2, backed by one DynamoDB Global Table and S3 cross-region
replication. Route 53 failover records handle hard crashes automatically. A scheduled
Lambda verifier performs deep health checks (DB roundtrip, error rate, disk) and feeds a
composite CloudWatch alarm; sustained degradation triggers an Step Functions state machine
that re-verifies, promotes the standby out of read-only mode, forces the DNS shift,
verifies the new primary, rolls back if verification fails, and finally replays any
in-flight writes found in the transactional outbox.

## Repository layout

```
app/                  FastAPI exam platform (+ chaos hooks, deep health, outbox writes, live board WebSockets)
web/                  Slate — TanStack Start frontend (classroom boards, quizzes, ops console)
lambdas/
  health_verifier/    Deep health probe -> CloudWatch metric / force checks for SFN
  failover_ops/       Promote standby, force Route 53 shift, replication status
  reconciler/         Outbox scan + idempotent replay after failover
statemachine/         Step Functions ASL definition
infra/
  modules/            network, compute (reusable)
  core/               ap-south-1 stack + global data plane (DynamoDB GT, S3 CRR)
  standby/            ap-southeast-1 stack (network + compute only)
  control/            Route 53, Step Functions, ops/reconciler Lambdas, dashboards
chaos/                Fault injection CLI (hard-down, degrade, db-stall, blip)
scripts/              App bundling/deployment helpers (+ local dev stack)
docs/                 architecture, runbook, demo script
```

## Prerequisites

- AWS account with admin credentials configured (`aws configure` or env vars), MFA on root.
- Terraform >= 1.6 (`brew install hashicorp/tap/hashicorp`)
- Python 3.11+
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

Automated suite (no AWS account needed — AWS is mocked with moto):

```bash
python3 -m venv .venv && .venv/bin/pip install -r requirements-dev.txt
make test          # pytest + coverage, gated at 100% line+branch
```

Layers: unit/integration for `app/`, all three lambda handlers and the chaos
CLI; a mini Step Functions simulator executing the real ASL graph through
failover, rollback, false-alarm and catch paths; end-to-end T1-T5 style
journeys (`tests/e2e/`), including a tier that boots real uvicorn processes +
moto_server and drives them with the actual CLI; Terraform fmt/validate,
ASL structural checks and shell syntax checks (`tests/infra/`).

See `docs/demo-script.md` for the live T1-T5 demo scenarios.

## Frontend development (Slate)

`web/` is a TanStack Start app (React 19, Tailwind v4, shadcn/ui base-nova
theme) with three surfaces: live Excalidraw classroom boards synced over
WebSockets (`/board/{room}`), quiz authoring + submissions (`/teacher`), and a
Continuum ops console with chaos injection (`/ops`). Students take quizzes at
`/quiz/{examId}`; submissions are graded server-side on submit.

```bash
make web-install    # bun install
make api-dev        # terminal 1: moto DynamoDB + uvicorn on :8000 (no AWS needed)
make web-dev        # terminal 2: Vite dev server on :3000, /api + /ws proxied
make web-check      # typecheck + eslint + production build
```

Notes:
- Boards relay through `app/ws.py` (in-memory rooms); snapshots autosave to the
  browser's localStorage per room. Board state is ephemeral by design — it does
  not survive a full-cluster restart.
- Quiz authoring and chaos endpoints require the admin token (default
  `continuum-demo-token`, override with `ADMIN_TOKEN`).
- The student "draw" permission on a board is teacher-side and client-enforced;
  fine for classrooms, not adversarial audiences.

## Cost discipline

Run `make destroy-all` after demo windows. Expected steady cost if left running ~$16/mo;
with teardown between sessions ~$2-5/mo. Set a $15 billing alarm in the console on day one.
