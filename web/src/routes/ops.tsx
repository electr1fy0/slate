import { useEffect, useRef, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ActivityIcon,
  ArrowUpCircleIcon,
  BombIcon,
  KeyRoundIcon,
  RotateCcwIcon,
  ServerCogIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { useAdminToken } from "@/lib/admin-token"
import { requireAuth } from "@/lib/guard"
import type { AdminStatus } from "@/lib/types"
import { useMounted } from "@/hooks/use-mounted"
import { AppShell } from "@/components/layout/app-shell"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { InputGroup, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Slider } from "@/components/ui/slider"
import { Switch } from "@/components/ui/switch"

export const Route = createFileRoute("/ops")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: OpsPage,
  head: () => ({ meta: [{ title: "Ops console · Slate" }] }),
})

const REGION_LABELS: Record<string, string> = {
  "ap-south-1": "Mumbai",
  "ap-southeast-1": "Singapore",
}

function regionLabel(region: string): string {
  return REGION_LABELS[region] ?? region
}

function useOpsRefetch(): () => Promise<void> {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: ["ops-status"] })
}

function handleSliderValue(
  setter: (next: number) => void,
  fallback: number,
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

  const statusQuery = useQuery({
    queryKey: ["ops-status", adminToken],
    queryFn: () => api.status(adminToken),
    enabled: mounted && adminToken.length > 0,
    refetchInterval: 5000,
    retry: false,
  })

  const unauthorised = statusQuery.error instanceof ApiError && statusQuery.error.status === 403

  if (!adminToken || unauthorised) {
    return (
      <AppShell title="Ops console" description="Continuum control plane">
        <div className="mx-auto w-full max-w-md pt-10">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <KeyRoundIcon data-icon="inline-start" />
                Admin access
              </CardTitle>
              <CardDescription>
                Enter the platform's admin token to inspect health and inject chaos.
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
                  <FieldDescription>Saved in this browser only.</FieldDescription>
                </Field>
              </FieldGroup>
            </CardContent>
          </Card>
        </div>
      </AppShell>
    )
  }

  return (
    <AppShell title="Ops console" description="Continuum control plane">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
        {!mounted || statusQuery.isPending ? (
          <div className="grid gap-4 md:grid-cols-3">
            {[0, 1, 2].map((index) => (
              <Skeleton key={index} className="h-32 rounded-xl" />
            ))}
          </div>
        ) : statusQuery.isError ? (
          <Empty className="rounded-xl border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ServerCogIcon />
              </EmptyMedia>
              <EmptyTitle>Status unavailable</EmptyTitle>
              <EmptyDescription>Is the API running? Try `make api-dev`.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            <StatusCards status={statusQuery.data} />
            <ChaosConsole token={adminToken} chaos={statusQuery.data.chaos} />
            <FailoverCard token={adminToken} role={statusQuery.data.role} />
          </>
        )}
      </div>
    </AppShell>
  )
}

function StatusCards({ status }: { status: AdminStatus }) {
  return (
    <div className="grid gap-4 md:grid-cols-3">
      <Card>
        <CardHeader>
          <CardDescription>Region</CardDescription>
          <CardTitle className="flex items-center gap-2 text-xl">
            {regionLabel(status.region)}
            <Badge variant="secondary">{status.region}</Badge>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Badge variant={status.role === "active" ? "default" : "secondary"}>
            {status.role.toUpperCase()}
            {status.role === "standby" ? " · read-only" : ""}
          </Badge>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardDescription>Live boards</CardDescription>
          <CardTitle className="text-xl">{status.boards.active_rooms}</CardTitle>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          WebSocket rooms served by this node.
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardDescription>Chaos state</CardDescription>
          <CardTitle className="text-xl">
            {status.chaos.error_pct}% err · {status.chaos.latency_ms}ms
            {status.chaos.db_stall ? " · DB stall" : ""}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Badge variant={status.chaos.db_stall || status.chaos.error_pct > 0 ? "destructive" : "secondary"}>
            {status.chaos.db_stall || status.chaos.error_pct > 0 ? "degraded" : "stable"}
          </Badge>
        </CardContent>
      </Card>
    </div>
  )
}

function ChaosConsole({
  token,
  chaos,
}: {
  token: string
  chaos: AdminStatus["chaos"]
}) {
  const queryClient = useOpsRefetch()
  const [errorPct, setErrorPct] = useState(chaos.error_pct)
  const [latencyMs, setLatencyMs] = useState(chaos.latency_ms)
  const [dbStall, setDbStall] = useState(chaos.db_stall)
  // While the teacher is dragging controls the panel is authoritative; once an
  // apply/reset lands (or another tab mutates state) re-sync from the server.
  const dirtyRef = useRef(false)

  useEffect(() => {
    if (dirtyRef.current) return
    setErrorPct(chaos.error_pct)
    setLatencyMs(chaos.latency_ms)
    setDbStall(chaos.db_stall)
  }, [chaos])

  function markDirty<T>(setter: (next: T) => void): (next: T) => void {
    return (next) => {
      dirtyRef.current = true
      setter(next)
    }
  }

  const applyMutation = useMutation({
    mutationFn: () => api.setChaos(token, { error_pct: errorPct, latency_ms: latencyMs, db_stall: dbStall }),
    onSuccess: () => {
      dirtyRef.current = false
      toast.success("Chaos applied")
      void queryClient()
    },
    onError: () => toast.error("Could not apply chaos"),
  })

  const resetMutation = useMutation({
    mutationFn: () => api.resetChaos(token),
    onSuccess: () => {
      dirtyRef.current = false
      setErrorPct(0)
      setLatencyMs(0)
      setDbStall(false)
      toast.info("Chaos cleared")
      void queryClient()
    },
    onError: () => toast.error("Could not clear chaos"),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <BombIcon data-icon="inline-start" />
          Chaos console
        </CardTitle>
        <CardDescription>
          Fault injection drives the failover demo — degrade, delay, or stall the data layer.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <Field>
          <FieldLabel>Error rate — {errorPct}%</FieldLabel>
          <Slider
            value={[errorPct]}
            onValueChange={handleSliderValue(markDirty(setErrorPct), errorPct)}
            min={0}
            max={100}
            step={5}
          />
        </Field>
        <Field>
          <FieldLabel>Added latency — {latencyMs} ms</FieldLabel>
          <Slider
            value={[latencyMs]}
            onValueChange={handleSliderValue(markDirty(setLatencyMs), latencyMs)}
            min={0}
            max={5000}
            step={100}
          />
        </Field>
        <label className="flex items-center justify-between rounded-lg border p-3">
          <span className="flex flex-col">
            <span className="text-sm font-medium">DynamoDB stall</span>
            <span className="text-xs text-muted-foreground">
              Deep health checks fail; triggers auto-failover.
            </span>
          </span>
          <Switch
            checked={dbStall}
            onCheckedChange={(checked) => markDirty(setDbStall)(checked)}
          />
        </label>
      </CardContent>
      <CardFooter className="gap-2">
        <Button onClick={() => applyMutation.mutate()} disabled={applyMutation.isPending}>
          <ActivityIcon data-icon="inline-start" />
          Apply chaos
        </Button>
        <Button variant="outline" onClick={() => resetMutation.mutate()} disabled={resetMutation.isPending}>
          <RotateCcwIcon data-icon="inline-start" />
          Reset
        </Button>
      </CardFooter>
    </Card>
  )
}

function FailoverCard({ token, role }: { token: string; role: AdminStatus["role"] }) {
  const queryClient = useOpsRefetch()

  const promoteMutation = useMutation({
    mutationFn: () => api.promote(token),
    onSuccess: (result) => {
      toast.success(`Node promoted — ${result.role}`)
      void queryClient()
    },
    onError: () => toast.error("Promotion failed"),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ArrowUpCircleIcon data-icon="inline-start" />
          Failover
        </CardTitle>
        <CardDescription>
          Promote this standby node to active. Serving traffic resumes immediately.
        </CardDescription>
      </CardHeader>
      <CardFooter>
        <AlertDialog>
          <AlertDialogTrigger
            render={
              <Button variant={role === "standby" ? "default" : "outline"} disabled={role !== "standby"}>
                Promote to active
              </Button>
            }
          />
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Promote this node?</AlertDialogTitle>
              <AlertDialogDescription>
                The node flips its role file to active and starts serving exams and boards.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction onClick={() => promoteMutation.mutate()}>Promote</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardFooter>
    </Card>
  )
}
