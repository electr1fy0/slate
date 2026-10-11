import { useMemo } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ActivityIcon,
  ArrowLeftRightIcon,
  BombIcon,
  CheckCircle2Icon,
  Clock3Icon,
  CloudIcon,
  DatabaseIcon,
  KeyRoundIcon,
  LockKeyholeIcon,
  RotateCcwIcon,
  SendIcon,
  ServerIcon,
  ShieldCheckIcon,
  ShieldXIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { useAdminToken } from "@/lib/admin-token"
import { requireAuth } from "@/lib/guard"
import type {
  ContinuumAction,
  ContinuumBackendStatus,
  ContinuumStatus,
} from "@/lib/types"
import { useMounted } from "@/hooks/use-mounted"
import { AppShell } from "@/components/layout/app-shell"
import { Badge } from "@/components/ui/badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { InputGroup, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"

export const Route = createFileRoute("/ops")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: OpsPage,
  head: () => ({ meta: [{ title: "Application recovery · Continuum" }] }),
})

const REGION_LABELS: Record<string, string> = {
  "ap-southeast-2": "Sydney",
  "ap-south-1": "Mumbai",
  "ap-southeast-1": "Singapore",
}

function regionLabel(region: string): string {
  return REGION_LABELS[region] ?? region
}

function isContinuumStatus(value: unknown): value is ContinuumStatus {
  return Boolean(
    value &&
      typeof value === "object" &&
      "epoch" in value &&
      "active_backend" in value &&
      "backends" in value &&
      Array.isArray(value.backends),
  )
}

function isAcceptedAction(value: unknown): value is { accepted: true } {
  return (
    typeof value === "object" &&
    value !== null &&
    (value as { accepted?: unknown }).accepted === true
  )
}

export function handleSliderValue(
  setter: (next: number) => void,
  fallback: number
): (value: number | readonly number[]) => void {
  return (value) => {
    const next = Array.isArray(value) ? value[0] : value
    if (typeof next === "number") setter(next)
    else setter(fallback)
  }
}

function OpsPage() {
  const mounted = useMounted()
  const [adminToken, saveAdminToken] = useAdminToken()
  const queryClient = useQueryClient()
  const queryKey = ["continuum-status", adminToken] as const

  const statusQuery = useQuery({
    queryKey,
    queryFn: () => api.continuumStatus(adminToken),
    enabled: mounted && adminToken.length > 0,
    refetchInterval: 5000,
    retry: false,
  })

  const actionMutation = useMutation({
    mutationFn: ({
      action,
      backend,
    }: {
      action: ContinuumAction
      backend?: string
    }) => api.continuumAction(adminToken, action, backend),
    onSuccess: (result, variables) => {
      const label =
        variables.action === "crash"
          ? statusQuery.data?.mode === "aws"
            ? "EC2 instance stop"
            : "Local HTTP server stop"
          : ACTION_LABELS[variables.action]
      if (isContinuumStatus(result)) {
        queryClient.setQueryData(queryKey, result)
        toast.success(`${label} recorded`)
      } else {
        void queryClient.invalidateQueries({ queryKey })
        const accepted = isAcceptedAction(result)
        toast.info(
          accepted
            ? `${label} accepted; refreshing observed status`
            : `${label} completed; refreshing observed status`,
        )
      }
    },
    onError: (error) => {
      toast.error(
        error instanceof Error ? error.message : "Control action failed"
      )
      void queryClient.invalidateQueries({ queryKey })
    },
  })

  const unauthorised =
    statusQuery.error instanceof ApiError && statusQuery.error.status === 403

  if (!adminToken || unauthorised) {
    return (
      <AppShell title="Continuum" description="Exam resilience cockpit">
        <div className="mx-auto w-full max-w-md pt-10">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <KeyRoundIcon data-icon="inline-start" />
                Admin access
              </CardTitle>
              <CardDescription>
                Enter the control token to inspect real backend health and run
                the bounded exam-failure scenarios.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="ops-token">Admin token</FieldLabel>
                  <InputGroup>
                    <InputGroupInput
                      id="ops-token"
                      type="password"
                      placeholder="Paste admin token"
                      value={adminToken}
                      onChange={(event) => saveAdminToken(event.target.value)}
                    />
                  </InputGroup>
                  <FieldDescription>
                    Saved in this browser only. The token is sent with status
                    and control requests.
                  </FieldDescription>
                </Field>
                {unauthorised ? (
                  <p role="alert" className="text-sm text-destructive">
                    That token was rejected.
                  </p>
                ) : null}
              </FieldGroup>
            </CardContent>
          </Card>
        </div>
      </AppShell>
    )
  }

  return (
    <AppShell title="Continuum" description="Exam resilience cockpit">
      <div className="mx-auto flex w-full max-w-7xl flex-col gap-6 pb-8">
        {!mounted || statusQuery.isPending ? (
          <div className="grid gap-4 lg:grid-cols-3">
            <Skeleton className="h-48 rounded-xl lg:col-span-2" />
            <Skeleton className="h-48 rounded-xl" />
            <Skeleton className="h-52 rounded-xl lg:col-span-3" />
          </div>
        ) : statusQuery.isError ? (
          <Empty className="rounded-xl border border-dashed py-12">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ServerIcon />
              </EmptyMedia>
              <EmptyTitle>Control status unavailable</EmptyTitle>
              <EmptyDescription className="max-w-lg">
                The API did not return a Continuum status snapshot. Check the
                API origin and control service, then retry. The cockpit does not
                invent backend state when the service is offline.
              </EmptyDescription>
            </EmptyHeader>
            <Button
              variant="outline"
              onClick={() => void statusQuery.refetch()}
              disabled={statusQuery.isFetching}
            >
              <ActivityIcon data-icon="inline-start" />
              Retry status
            </Button>
          </Empty>
        ) : (
          <ContinuumCockpit
            status={statusQuery.data}
            busy={actionMutation.isPending}
            onAction={(action, backend) =>
              actionMutation.mutate({ action, backend })
            }
          />
        )}
      </div>
    </AppShell>
  )
}

const ACTION_LABELS: Record<ContinuumAction, string> = {
  crash: "Process failure",
  partial: "Capability failure",
  blip: "Short failure blip",
  unsafe_standby: "Unsafe standby fault",
  lost_response: "Lost response fault",
  reset: "Fault reset",
  verify: "Backend verification",
  failback: "Failback request",
  reconcile: "Reconciliation",
}

export function ContinuumCockpit({
  status,
  busy,
  onAction,
}: {
  status: ContinuumStatus
  busy: boolean
  onAction: (action: ContinuumAction, backend?: string) => void
}) {
  const activeBackend = status.backends.find(
    (backend) => backend.id === status.active_backend
  )
  const standbyBackend = status.backends.find(
    (backend) => backend.id !== status.active_backend
  )
  const timeline = useMemo(
    () =>
      [...status.events].sort(
        (left, right) => Date.parse(right.at) - Date.parse(left.at)
      ),
    [status.events]
  )

  return (
    <>
      <section className="relative overflow-hidden rounded-2xl border bg-card p-5 shadow-sm md:p-7">
        <div className="pointer-events-none absolute -top-20 -right-14 size-64 rounded-full bg-primary/10 blur-3xl" />
        <div className="relative flex flex-col gap-6 lg:flex-row lg:items-end lg:justify-between">
          <div className="flex max-w-3xl flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2">
              <Badge
                variant={status.mode === "aws" ? "default" : "secondary"}
                className="tracking-wide uppercase"
              >
                <CloudIcon data-icon="inline-start" />
                {status.mode} control plane
              </Badge>
              <Badge variant={status.paused ? "destructive" : "outline"}>
                {status.paused ? "PAUSED" : "OBSERVING"}
              </Badge>
            </div>
            <div>
              <p className="mb-2 text-xs font-semibold tracking-[0.18em] text-muted-foreground uppercase">
                Application failure recovery
              </p>
              <h2 className="text-3xl font-semibold tracking-tight md:text-4xl">
                Protect the student’s submission path.
              </h2>
              <p className="mt-3 max-w-2xl text-sm leading-relaxed text-muted-foreground md:text-base">
                Continuum checks application capability, changes the active
                writer only after verification, and keeps the exam request safe
                to retry through a disruption.
              </p>
            </div>
          </div>
          <div className="grid min-w-64 grid-cols-2 gap-2 rounded-xl border bg-background/80 p-3">
            <div className="col-span-2 flex items-center justify-between border-b pb-2">
              <span className="text-xs text-muted-foreground">
                Selected Region
              </span>
              <span className="text-right text-sm font-semibold">
                {regionLabel(status.region)}
              </span>
            </div>
            <div className="pt-1">
              <span className="block text-xs text-muted-foreground">
                Active writer
              </span>
              <span className="text-lg font-semibold">
                Backend {status.active_backend}
              </span>
            </div>
            <div className="pt-1 text-right">
              <span className="block text-xs text-muted-foreground">
                Write epoch
              </span>
              <span className="font-mono text-lg font-semibold">
                {status.epoch}
              </span>
            </div>
          </div>
        </div>
      </section>

      {status.paused ? (
        <Alert variant="destructive">
          <TriangleAlertIcon />
          <AlertTitle>Control plane is paused</AlertTitle>
          <AlertDescription>
            Status is the last returned snapshot. Verify or resume the control
            service before interpreting fault actions.
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-12">
        <section
          className="flex flex-col gap-3 lg:col-span-8"
          aria-labelledby="backend-title"
        >
          <div className="flex items-end justify-between gap-3">
            <div>
              <p className="text-xs font-semibold tracking-[0.18em] text-muted-foreground uppercase">
                Live topology
              </p>
              <h3 id="backend-title" className="mt-1 text-xl font-semibold">
                Two application backends
              </h3>
            </div>
            <Badge variant="outline">
              <ActivityIcon data-icon="inline-start" /> Updated every 5s
            </Badge>
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            {status.backends.map((backend) => (
              <BackendCard
                key={backend.id}
                backend={backend}
                active={backend.id === status.active_backend}
                epoch={status.epoch}
              />
            ))}
            {status.backends.length === 0 ? (
              <Empty className="rounded-xl border border-dashed md:col-span-2">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <ServerIcon />
                  </EmptyMedia>
                  <EmptyTitle>No backend observations</EmptyTitle>
                  <EmptyDescription>
                    The control service returned an empty backend list.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : null}
          </div>
        </section>

        <section
          className="flex flex-col gap-3 lg:col-span-4"
          aria-labelledby="evidence-title"
        >
          <div>
            <p className="text-xs font-semibold tracking-[0.18em] text-muted-foreground uppercase">
              Observed evidence
            </p>
            <h3 id="evidence-title" className="mt-1 text-xl font-semibold">
              Policy & recovery
            </h3>
          </div>
          <div className="grid flex-1 gap-3 sm:grid-cols-2 lg:grid-cols-1">
            <Card>
              <CardHeader className="pb-3">
                <CardDescription>Intent policy</CardDescription>
                <CardTitle className="flex items-center gap-2 text-base">
                  <ShieldCheckIcon className="size-4 text-primary" />
                  Version {status.policy.version}
                </CardTitle>
              </CardHeader>
              <CardContent className="grid grid-cols-2 gap-3 text-sm">
                <Metric
                  label="Confirm probes"
                  value={status.policy.confirmation_probes}
                />
                <Metric
                  label="Probe interval"
                  value={`${status.policy.probe_interval_ms} ms`}
                />
              </CardContent>
            </Card>
            <div className="grid grid-cols-2 gap-3">
              <MetricCard
                label="Failovers"
                value={status.metrics.failovers}
                icon={<ArrowLeftRightIcon />}
              />
              <MetricCard
                label="Suppressed"
                value={status.metrics.suppressed}
                icon={<ShieldXIcon />}
              />
              <MetricCard
                label="Probe-to-verify"
                value={duration(status.metrics.recovery_ms)}
                icon={<Clock3Icon />}
              />
              <MetricCard
                label={status.mode === "aws" ? "Fenced writes · not reported" : "Fenced writes"}
                value={status.mode === "aws" ? "—" : optionalCount(status.metrics.fenced_writes)}
                icon={<LockKeyholeIcon />}
              />
            </div>
            {status.mode === "aws" ? (
              <p className="text-xs text-muted-foreground">
                AWS counters summarize the recent 50 control-plane events. Recovery duration starts at the first monitor probe and ends at public verification; schedule delay is additional. Fenced-write totals are not currently exposed.
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">
                Local counters cover this process run; the timeline retains its latest 100 events.
              </p>
            )}
            <Card size="sm">
              <CardContent className="flex items-center justify-between gap-3 pt-4 text-sm">
                <span className="text-muted-foreground">Reconciliation</span>
                <span className="font-medium">
                  {reconciliationLabel(status.metrics.reconciled)}
                </span>
              </CardContent>
            </Card>
          </div>
        </section>
      </div>

      <ActionsPanel
        activeBackend={activeBackend}
        standbyBackend={standbyBackend}
        mode={status.mode}
        busy={busy}
        onAction={onAction}
      />

      <div className="grid gap-4 lg:grid-cols-5">
        <PitchCard className="lg:col-span-2" />
        <EventTimeline className="lg:col-span-3" events={timeline} />
      </div>
    </>
  )
}

function BackendCard({
  backend,
  active,
  epoch,
}: {
  backend: ContinuumBackendStatus
  active: boolean
  epoch: number
}) {
  return (
    <Card
      className={active ? "border-primary/50 shadow-sm shadow-primary/5" : ""}
    >
      <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
        <div>
          <CardDescription>Application backend</CardDescription>
          <CardTitle className="mt-1 flex items-center gap-2 text-2xl">
            {backend.id}
            {active ? (
              <Badge>ACTIVE</Badge>
            ) : (
              <Badge variant="secondary">STANDBY</Badge>
            )}
          </CardTitle>
        </div>
        <BackendHealth healthy={backend.healthy} />
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-2">
          <HealthCheck label="Process liveness" passed={backend.liveness} />
          <HealthCheck label="Exam readiness" passed={backend.ready} />
        </div>
        <div className="flex items-start justify-between gap-3 border-t pt-3 text-xs">
          <span className="text-muted-foreground">
            {backend.reason || "No additional health detail returned."}
          </span>
          <span className="shrink-0 font-mono text-muted-foreground">
            epoch {active ? epoch : "fenced"}
          </span>
        </div>
      </CardContent>
    </Card>
  )
}

function BackendHealth({ healthy }: { healthy: boolean }) {
  return (
    <Badge
      variant={healthy ? "outline" : "destructive"}
      className={
        healthy
          ? "border-emerald-600/30 text-emerald-700 dark:text-emerald-400"
          : ""
      }
    >
      {healthy ? (
        <CheckCircle2Icon data-icon="inline-start" />
      ) : (
        <TriangleAlertIcon data-icon="inline-start" />
      )}
      {healthy ? "Healthy" : "Degraded"}
    </Badge>
  )
}

function HealthCheck({ label, passed }: { label: string; passed: boolean }) {
  return (
    <div className="flex items-center gap-2 rounded-lg border bg-muted/30 px-3 py-2 text-xs">
      {passed ? (
        <CheckCircle2Icon className="size-4 shrink-0 text-emerald-600" />
      ) : (
        <TriangleAlertIcon className="size-4 shrink-0 text-destructive" />
      )}
      <span className="flex flex-col">
        <span className="font-medium">{passed ? "Pass" : "Fail"}</span>
        <span className="text-muted-foreground">{label}</span>
      </span>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <span className="block text-xs text-muted-foreground">{label}</span>
      <span className="font-semibold tabular-nums">{value}</span>
    </div>
  )
}

function MetricCard({
  label,
  value,
  icon,
}: {
  label: string
  value: React.ReactNode
  icon: React.ReactNode
}) {
  return (
    <Card size="sm">
      <CardContent className="flex items-center gap-3 pt-4">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground [&>svg]:size-4">
          {icon}
        </span>
        <div className="min-w-0">
          <span className="block truncate text-xs text-muted-foreground">
            {label}
          </span>
          <span className="text-lg font-semibold tabular-nums">{value}</span>
        </div>
      </CardContent>
    </Card>
  )
}

function duration(ms: number | null): string {
  if (ms === null || !Number.isFinite(ms) || ms <= 0) return "—"
  return `${ms} ms`
}

function optionalCount(value: number | null | undefined): string | number {
  return value === null || value === undefined ? "—" : value
}

function reconciliationLabel(
  value: number | boolean | null | undefined
): string {
  if (typeof value === "boolean") return value ? "Complete" : "Pending"
  if (typeof value === "number") return `${value} processed`
  return "Not reported"
}

function ActionsPanel({
  activeBackend,
  standbyBackend,
  mode,
  busy,
  onAction,
}: {
  activeBackend?: ContinuumBackendStatus
  standbyBackend?: ContinuumBackendStatus
  mode: string
  busy: boolean
  onAction: (action: ContinuumAction, backend?: string) => void
}) {
  return (
    <Card>
      <CardHeader className="gap-1">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <BombIcon data-icon="inline-start" /> Faculty scenario controls
            </CardTitle>
            <CardDescription className="mt-1">
              Each control calls the Continuum API. Health changes and recovery
              decisions appear in the timeline after the service reports them.
            </CardDescription>
          </div>
          {busy ? (
            <Badge variant="secondary">
              <ActivityIcon data-icon="inline-start" /> Running action
            </Badge>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
          <ScenarioButton
            action="crash"
            backend={activeBackend?.id}
            label={`${mode === "aws" ? "Stop EC2 instance" : "Stop local HTTP server"} · ${activeBackend?.id ?? "—"}`}
            detail={mode === "aws" ? "AWS StopInstances" : "Close application listener"}
            disabled={busy || !activeBackend}
            onAction={onAction}
          />
          <ScenarioButton
            action="partial"
            backend={activeBackend?.id}
            label={`Partial failure · ${activeBackend?.id ?? "—"}`}
            detail="Liveness stays up"
            disabled={busy || !activeBackend}
            onAction={onAction}
          />
          <ScenarioButton
            action="blip"
            backend={activeBackend?.id}
            label={`Short blip · ${activeBackend?.id ?? "—"}`}
            detail="Check suppression policy"
            disabled={busy || !activeBackend}
            onAction={onAction}
          />
          <ScenarioButton
            action="unsafe_standby"
            label={`Unsafe standby · ${standbyBackend?.id ?? "—"}`}
            detail="Promotion should be refused"
            disabled={busy || !standbyBackend}
            onAction={onAction}
          />
          <ScenarioButton
            action="lost_response"
            backend={activeBackend?.id}
            label={`Drop receipt · ${activeBackend?.id ?? "—"}`}
            detail="Commit then return 503"
            icon={<SendIcon />}
            disabled={busy || !activeBackend}
            onAction={onAction}
          />
        </div>
        <div className="flex flex-wrap gap-2 border-t pt-4">
          <Button
            variant="outline"
            onClick={() => onAction("verify")}
            disabled={busy}
          >
            <ShieldCheckIcon data-icon="inline-start" /> Verify backends
          </Button>
          <Button
            variant="outline"
            onClick={() => onAction("failback")}
            disabled={busy}
          >
            <ArrowLeftRightIcon data-icon="inline-start" /> Request failback
          </Button>
          <Button
            variant="outline"
            onClick={() => onAction("reconcile")}
            disabled={busy}
          >
            <DatabaseIcon data-icon="inline-start" /> Reconcile pending work
          </Button>
          <Button
            variant="outline"
            className="ml-auto"
            onClick={() => onAction("reset")}
            disabled={busy}
          >
            <RotateCcwIcon data-icon="inline-start" /> Reset injected faults
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function ScenarioButton({
  action,
  backend,
  label,
  detail,
  icon,
  disabled,
  onAction,
}: {
  action: ContinuumAction
  backend?: string
  label: string
  detail: string
  icon?: React.ReactNode
  disabled: boolean
  onAction: (action: ContinuumAction, backend?: string) => void
}) {
  return (
    <Button
      variant="outline"
      className="h-auto min-h-20 flex-col items-start justify-center gap-1 px-3 py-3 text-left whitespace-normal"
      aria-label={label}
      disabled={disabled}
      onClick={() => onAction(action, backend)}
    >
      <span className="flex items-center gap-2 text-sm font-semibold">
        {icon ?? <BombIcon className="size-4" />}
        {label}
      </span>
      <span className="pl-6 text-xs font-normal text-muted-foreground">
        {detail}
      </span>
    </Button>
  )
}

function PitchCard({ className = "" }: { className?: string }) {
  return (
    <Card className={className}>
      <CardHeader>
        <CardDescription>What Continuum adds</CardDescription>
        <CardTitle className="text-lg">
          Capability checks, fenced writes
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 text-sm leading-relaxed">
        <p className="text-muted-foreground">
          A process can answer a basic health check while the exam or database
          path is broken. Continuum checks application readiness as well as
          liveness before it changes the active backend.
        </p>
        <div className="flex gap-3 rounded-lg border bg-muted/30 p-3">
          <LockKeyholeIcon className="mt-0.5 size-4 shrink-0 text-primary" />
          <p>
            Critical exam writes check the epoch inside their transaction. A
            stale backend cannot commit those writes after promotion.
          </p>
        </div>
        <div className="flex gap-3 rounded-lg border bg-muted/30 p-3">
          <CloudIcon className="mt-0.5 size-4 shrink-0 text-primary" />
          <p>
            For this low-traffic demo, a pay-per-request router keeps the choice
            visible without an always-running load balancer.
          </p>
        </div>
        <p className="border-t pt-3 text-xs text-muted-foreground">
          Scope: application failure recovery inside one Region. This demo does
          not establish full-Region outage protection or near-zero recovery
          time.
        </p>
      </CardContent>
    </Card>
  )
}

function EventTimeline({
  className = "",
  events,
}: {
  className?: string
  events: ContinuumStatus["events"]
}) {
  return (
    <Card className={className}>
      <CardHeader className="flex-row items-end justify-between gap-3 space-y-0">
        <div>
          <CardDescription>Control-plane evidence</CardDescription>
          <CardTitle className="mt-1 flex items-center gap-2 text-lg">
            <ActivityIcon className="size-4" /> Event timeline
          </CardTitle>
        </div>
        <Badge variant="outline">{events.length} events</Badge>
      </CardHeader>
      <CardContent>
        {events.length === 0 ? (
          <div className="rounded-lg border border-dashed px-4 py-8 text-center">
            <p className="text-sm font-medium">No events recorded yet</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Run a scenario to capture health, decision, and recovery evidence.
            </p>
          </div>
        ) : (
          <ol className="flex flex-col divide-y">
            {events.map((event) => (
              <li
                key={event.id}
                className="grid grid-cols-[auto_1fr] gap-3 py-3 first:pt-0 last:pb-0"
              >
                <span className="mt-0.5 flex size-8 items-center justify-center rounded-full bg-muted text-muted-foreground">
                  {event.type.toLowerCase().includes("fail") ? (
                    <TriangleAlertIcon className="size-4" />
                  ) : (
                    <CheckCircle2Icon className="size-4" />
                  )}
                </span>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary" className="capitalize">
                      {event.type.replaceAll("_", " ")}
                    </Badge>
                    {event.backend ? (
                      <Badge variant="outline">Backend {event.backend}</Badge>
                    ) : null}
                    {event.epoch !== undefined ? (
                      <span className="font-mono text-xs text-muted-foreground">
                        epoch {event.epoch}
                      </span>
                    ) : null}
                    <time
                      className="ml-auto text-xs text-muted-foreground"
                      dateTime={event.at}
                    >
                      {formatTime(event.at)}
                    </time>
                  </div>
                  <p className="mt-1 text-sm leading-relaxed">
                    {event.message}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        )}
      </CardContent>
    </Card>
  )
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf())
    ? value
    : date.toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      })
}
