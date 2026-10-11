import { useEffect } from "react"
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { api } from "@/lib/api"
import { setAuth } from "@/lib/auth"
import BoardCanvas from "./board-canvas"
import type { BoardCanvasHandle } from "./board-canvas"
import { BoardHistoryDialog } from "./board-history-dialog"

const canvas = vi.hoisted(() => ({ updateScene: vi.fn(), change: null as null | ((elements: unknown[], state: unknown) => void) }))
vi.mock("@excalidraw/excalidraw", () => ({
  CaptureUpdateAction: { NEVER: "never" },
  reconcileElements: (_local: unknown[], remote: unknown[]) => remote,
  Excalidraw: ({ onChange, excalidrawAPI }: { onChange: (elements: unknown[], state: unknown) => void; excalidrawAPI: (api: unknown) => void }) => {
    canvas.change = onChange
    useEffect(() => { excalidrawAPI({ updateScene: canvas.updateScene }) }, [excalidrawAPI])
    return <div data-testid="canvas" />
  },
}))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

beforeEach(() => {
  canvas.updateScene.mockClear()
  setAuth({ user: { user_id: "teacher", name: "Teacher", email: "t@example.com", role: "teacher" }, accessToken: "token", refreshToken: null })
})
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })

describe("board snapshot continuity", () => {
  it("replaces the whole scene on restore, even when historical element revisions are older", () => {
    const handle = { current: { mergeRemote: undefined, getSnapshot: undefined } as BoardCanvasHandle }
    const send = vi.fn()
    const { rerender } = render(<BoardCanvas roomId="room" readOnly={false} handle={handle} senderRef={{ current: send }} />)
    act(() => canvas.change?.([{ id: "new", version: 8 }], {}))
    const historical = [{ id: "old", version: 1 }]
    rerender(<BoardCanvas roomId="room" readOnly={false} handle={handle} senderRef={{ current: send }} remoteSnapshot={historical} />)
    expect(handle.current.getSnapshot?.()).toEqual(historical)
    expect(canvas.updateScene).toHaveBeenLastCalledWith({ elements: historical, captureUpdate: "never" })
  })

  it("relays deletion tombstones and retains them in autosaved snapshots", async () => {
    vi.useFakeTimers()
    const handle = { current: { mergeRemote: undefined, getSnapshot: undefined } as BoardCanvasHandle }
    const send = vi.fn()
    render(<BoardCanvas roomId="room" readOnly={false} handle={handle} senderRef={{ current: send }} />)
    act(() => canvas.change?.([{ id: "shape", version: 2, isDeleted: true }], {}))
    act(() => vi.advanceTimersByTime(900))
    expect(send).toHaveBeenCalledWith({ type: "elements", elements: [{ id: "shape", version: 2, isDeleted: true }] })
    expect(JSON.parse(window.localStorage.getItem("slate.board.room")!)).toEqual([{ id: "shape", version: 2, isDeleted: true }])
    act(() => handle.current.mergeRemote?.([{ id: "remote", version: 3, isDeleted: true }]))
    expect(canvas.updateScene).toHaveBeenLastCalledWith({ elements: [{ id: "remote", version: 3, isDeleted: true }], captureUpdate: "never" })
  })
})

function history(onRestored = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><BoardHistoryDialog roomId="room" enabled onRestored={onRestored} /></QueryClientProvider>)
}

describe("board version history", () => {
  it("requires confirmation and applies a successful restore", async () => {
    vi.spyOn(api, "boardHistory").mockResolvedValue([{ room_id: "room", version: 1, element_count: 1 }])
    const restore = vi.spyOn(api, "boardRestore").mockResolvedValue({ room_id: "room", version: 3, elements: [{ id: "historical" }] })
    const replaced = vi.fn()
    history(replaced)
    fireEvent.click(screen.getByRole("button", { name: "History" }))
    fireEvent.click(await screen.findByRole("button", { name: "Restore" }))
    expect(restore).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole("button", { name: "Restore" }))
    fireEvent.click(screen.getByRole("button", { name: "Restore snapshot" }))
    await waitFor(() => expect(replaced).toHaveBeenCalledWith([{ id: "historical" }]))
    expect(restore).toHaveBeenCalledWith("room", 1)
  })

  it("shows history errors with retry and gives learners a read-only history", async () => {
    setAuth({ user: { user_id: "student", name: "Student", email: "s@example.com", role: "student" }, accessToken: "token", refreshToken: null })
    vi.spyOn(api, "boardHistory").mockRejectedValueOnce(new Error("offline")).mockResolvedValue([{ room_id: "room", version: 2, element_count: 0 }])
    history()
    fireEvent.click(screen.getByRole("button", { name: "History" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load version history")
    fireEvent.click(screen.getByRole("button", { name: "Retry history" }))
    expect(await screen.findByText("Version 2 · 0 elements")).toBeVisible()
    expect(screen.queryByRole("button", { name: "Restore" })).not.toBeInTheDocument()
  })
})
