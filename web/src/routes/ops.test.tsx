import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import type { ContinuumStatus } from "@/lib/types"

import { ContinuumCockpit } from "./ops"

const STATUS: ContinuumStatus = {
  mode: "local",
  region: "ap-southeast-2",
  active_backend: "A",
  epoch: 4,
  paused: false,
  policy: { version: "v1", confirmation_probes: 3, probe_interval_ms: 5000 },
  backends: [
    {
      id: "A",
      healthy: true,
      liveness: true,
      ready: true,
      reason: "Exam write and read probes pass.",
    },
    {
      id: "B",
      healthy: true,
      liveness: true,
      ready: true,
      reason: "Standby capability probe passes.",
    },
  ],
  events: [
    {
      id: "evt-1",
      at: "2026-10-10T12:00:00Z",
      type: "baseline",
      message: "Both application paths verified.",
      epoch: 4,
    },
  ],
  metrics: {
    failovers: 1,
    suppressed: 1,
    recovery_ms: 730,
    reconciled: 2,
    fenced_writes: 1,
  },
}

describe("Continuum cockpit", () => {
  it("shows live capability evidence and sends the selected active-backend fault", () => {
    const onAction = vi.fn()
    render(
      <ContinuumCockpit status={STATUS} busy={false} onAction={onAction} />
    )

    expect(screen.getByText("Sydney")).toBeInTheDocument()
    expect(screen.getByText("Write epoch").parentElement).toHaveTextContent("4")
    expect(screen.getByText("Probe-to-verify")).toBeInTheDocument()
    expect(
      screen.getByText("Exam write and read probes pass.")
    ).toBeInTheDocument()
    expect(screen.getByText("Event timeline")).toBeInTheDocument()
    expect(
      screen.getByText("Capability checks, fenced writes")
    ).toBeInTheDocument()
    expect(
      screen.getByText(/does not establish full-Region outage protection/)
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Partial failure · A" }))

    expect(onAction).toHaveBeenCalledWith("partial", "A")
  })

  it("routes the unsafe standby fault without a backend override", () => {
    const onAction = vi.fn()
    render(
      <ContinuumCockpit status={STATUS} busy={false} onAction={onAction} />
    )

    fireEvent.click(screen.getByRole("button", { name: "Unsafe standby · B" }))

    expect(onAction).toHaveBeenCalledWith("unsafe_standby", undefined)
  })

  it("names the process action according to the active control mode", () => {
    const view = render(
      <ContinuumCockpit status={STATUS} busy={false} onAction={vi.fn()} />,
    )
    expect(screen.getByRole("button", { name: "Stop local HTTP server · A" })).toBeInTheDocument()

    view.rerender(
      <ContinuumCockpit
        status={{ ...STATUS, mode: "aws" }}
        busy={false}
        onAction={vi.fn()}
      />,
    )
    expect(screen.getByRole("button", { name: "Stop EC2 instance · A" })).toBeInTheDocument()
  })
})
