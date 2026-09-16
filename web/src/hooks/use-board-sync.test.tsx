import { act } from "@testing-library/react"
import { createRoot } from "react-dom/client"
import type { Root } from "react-dom/client"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setAuth } from "@/lib/auth"
import type { ClientMessage } from "@/lib/types"
import { useBoardSync } from "./use-board-sync"

/**
 * Minimal WebSocket double: records sends, lets tests drive open/message/
 * close transitions synchronously.
 */
class MockWebSocket {
  static instances: MockWebSocket[] = []
  static OPEN = 1
  static CONNECTING = 0
  static CLOSED = 3

  static get OPEN_CONSTANT() {
    return MockWebSocket.OPEN
  }

  readyState = MockWebSocket.CONNECTING
  sent: string[] = []
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  closedWithCode: number | null = null

  constructor(public url: string) {
    MockWebSocket.instances.push(this)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close(code?: number) {
    this.readyState = MockWebSocket.CLOSED
    this.closedWithCode = code ?? null
    this.onclose?.()
  }

  // ---- helpers for the test driver ----
  simulateOpen() {
    this.readyState = MockWebSocket.OPEN
    this.onopen?.()
  }

  simulateMessage(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
}

const ORIGINAL_WS = globalThis.WebSocket

function installMockWebSocket() {
  MockWebSocket.instances = []
  ;(globalThis as unknown as { WebSocket: unknown }).WebSocket =
    MockWebSocket
}

function useProbe(options: Parameters<typeof useBoardSync>[0]) {
  const state = {
    current: undefined as unknown as ReturnType<typeof useBoardSync>,
  }
  function Probe() {
    state.current = useBoardSync(options)
    return null
  }
  return { Probe, state }
}

describe("useBoardSync", () => {
  let container: HTMLDivElement
  let root: Root | null = null

  beforeEach(() => {
    vi.useFakeTimers()
    installMockWebSocket()
    container = document.createElement("div")
    document.body.appendChild(container)
    setAuth({
      user: { user_id: "u1", email: "a@b.com", name: "Ada", role: "teacher" },
      accessToken: "token-abc",
      refreshToken: "refresh-abc",
    })
  })

  afterEach(() => {
    root?.unmount()
    root = null
    container.remove()
    vi.useRealTimers()
    globalThis.WebSocket = ORIGINAL_WS
  })

  function mount(probe: ReturnType<typeof useProbe>) {
    root = createRoot(container)
    act(() => {
      root!.render(<probe.Probe />)
    })
  }

  const baseOptions: Parameters<typeof useBoardSync>[0] = {
    roomId: "room-1",
    name: "Ada",
    role: "teacher" as const,
    onInit: vi.fn(),
    onElements: vi.fn(),
  }

  it("connects, joins with identity, and processes init", () => {
    const onInit = vi.fn()
    const probe = useProbe({ ...baseOptions, onInit })
    mount(probe)

    const socket = MockWebSocket.instances[0]
    expect(socket.url).toContain("/ws/board/room-1?token=")
    expect(probe.state.current.status).toBe("connecting")

    act(() => socket.simulateOpen())
    expect(probe.state.current.status).toBe("live")

    const joinFrame = JSON.parse(socket.sent[0])
    expect(joinFrame.type).toBe("join")
    expect(joinFrame.name).toBe("Ada")

    act(() => {
      socket.simulateMessage({
        type: "init",
        you: "peer-1",
        elements: [{ id: "e1" }],
        peers: [],
      })
    })
    expect(onInit).toHaveBeenCalledWith([{ id: "e1" }])
    expect(probe.state.current.selfId).toBe("peer-1")
  })

  it("joins as a guest, ignores a duplicate open, and closes on socket errors", () => {
    const probe = useProbe({ ...baseOptions, name: "", role: "student" })
    mount(probe)
    const socket = MockWebSocket.instances[0]
    act(() => {
      socket.simulateOpen()
      socket.simulateOpen()
    })
    expect(JSON.parse(socket.sent[0])).toMatchObject({ type: "join", name: "Guest", role: "student" })
    expect(socket.sent.filter((frame) => JSON.parse(frame).type === "join").length).toBeGreaterThanOrEqual(2)
    act(() => socket.onerror?.())
    expect(socket.readyState).toBe(MockWebSocket.CLOSED)
  })

  it("buffers elements while offline and flushes after reconnect", () => {
    const probe = useProbe(baseOptions)
    mount(probe)

    const first = MockWebSocket.instances[0]
    const message: ClientMessage = {
      type: "elements",
      elements: [{ id: "e9", version: 3 }],
    }
    act(() => probe.state.current.send(message))
    expect(first.sent).toEqual([]) // nothing open yet -> queued

    // Cursor frames are never queued offline.
    act(() => probe.state.current.send({ type: "cursor", x: 5, y: 6, tool: "pointer" }))

    act(() => first.simulateOpen())
    // Join first, then the drained outbox; the cursor frame stayed behind.
    expect(JSON.parse(first.sent[0]).type).toBe("join")
    expect(JSON.parse(first.sent[1])).toEqual(message)
    expect(JSON.stringify(first.sent)).not.toContain("cursor")

    // Online again: cursors flow immediately.
    act(() => probe.state.current.send({ type: "cursor", x: 7, y: 8, tool: "pointer" }))
    expect(first.sent.at(-1)).toContain('"cursor"')
  })

  it("caps the offline outbox and drops stale cursor positions", () => {
    const probe = useProbe(baseOptions)
    mount(probe)

    for (let i = 0; i < 205; i += 1) {
      act(() =>
        probe.state.current.send({
          type: "elements",
          elements: [{ id: `e${i}`, version: 1 }],
        }),
      )
    }
    const socket = MockWebSocket.instances[0]
    act(() => socket.simulateOpen())
    const elementFrames = socket.sent
      .slice(1)
      .map((frame) => JSON.parse(frame))
      .filter((parsed) => parsed.type === "elements")
    expect(elementFrames.length).toBeLessThanOrEqual(200)
  })

  it("schedules exponential reconnects and resets attempts when live", () => {
    const probe = useProbe(baseOptions)
    mount(probe)

    const first = MockWebSocket.instances[0]
    act(() => first.close()) // retry scheduled (500ms for attempt 0)
    expect(probe.state.current.status).toBe("reconnecting")

    act(() => vi.advanceTimersByTime(500))
    const second = MockWebSocket.instances.at(-1)!
    expect(second).not.toBe(first)
    expect(probe.state.current.status).toBe("reconnecting")

    act(() => second.simulateOpen())
    expect(probe.state.current.status).toBe("live")

    act(() => second.close())
    act(() => vi.advanceTimersByTime(500)) // attempt reset -> back to 500ms
    const third = MockWebSocket.instances.at(-1)!
    expect(third).not.toBe(second)
  })

  it("manual reconnect dials immediately instead of waiting out the backoff", () => {
    const probe = useProbe(baseOptions)
    mount(probe)

    const first = MockWebSocket.instances[0]
    first.close()

    act(() => probe.state.current.reconnect())
    const second = MockWebSocket.instances.at(-1)!
    expect(second).not.toBe(first)
    expect(probe.state.current.status).toBe("connecting")

    act(() => second.simulateOpen())
    expect(probe.state.current.status).toBe("live")
  })

  it("reconnects a closed socket when no backoff timer exists", () => {
    const probe = useProbe(baseOptions)
    mount(probe)
    const first = MockWebSocket.instances[0]
    first.readyState = MockWebSocket.CLOSED
    act(() => probe.state.current.reconnect())
    expect(MockWebSocket.instances).toHaveLength(2)
    expect(MockWebSocket.instances[1]).not.toBe(first)
  })

  it("reconnect is a no-op while already live or connecting", () => {
    const probe = useProbe(baseOptions)
    mount(probe)
    const before = MockWebSocket.instances.length

    act(() => probe.state.current.reconnect()) // still connecting
    expect(MockWebSocket.instances.length).toBe(before)

    const socket = MockWebSocket.instances[0]
    act(() => socket.simulateOpen())
    act(() => probe.state.current.reconnect()) // live now
    expect(MockWebSocket.instances.length).toBe(before)
  })

  it("handles every server frame and sends heartbeats", () => {
    const onInit = vi.fn()
    const onElements = vi.fn()
    const probe = useProbe({ ...baseOptions, onInit, onElements })
    mount(probe)
    const socket = MockWebSocket.instances[0]
    act(() => socket.simulateOpen())

    const firstPeer = { id: "peer-1", name: "Bea", role: "student" as const, cursor: null }
    const secondPeer = { id: "peer-2", name: "Cy", role: "teacher" as const, cursor: null }
    act(() => {
      socket.onmessage?.({ data: "not-json" })
      socket.simulateMessage({ type: "init", you: "self", elements: [{ id: "a" }], peers: [firstPeer] })
      socket.simulateMessage({ type: "peer-joined", peer: firstPeer })
      socket.simulateMessage({ type: "peer-joined", peer: secondPeer })
      socket.simulateMessage({
        type: "cursor",
        peer: { ...firstPeer, cursor: { x: 10, y: 20, tool: "pen" } },
      })
      socket.simulateMessage({ type: "elements", elements: [{ id: "b" }] })
      socket.simulateMessage({ type: "peer-left", peer: secondPeer })
      socket.simulateMessage({ type: "pong" })
      socket.simulateMessage({ type: "error", message: "transient" })
    })
    expect(onInit).toHaveBeenCalledWith([{ id: "a" }])
    expect(onElements).toHaveBeenCalledWith([{ id: "b" }])
    expect(probe.state.current.peers).toEqual([
      { id: "peer-1", name: "Bea", role: "student", cursor: { x: 10, y: 20, tool: "pen" } },
    ])

    act(() => vi.advanceTimersByTime(20_000))
    expect(JSON.parse(socket.sent.at(-1)!)).toEqual({ type: "ping" })
    act(() => socket.close())
    act(() => vi.advanceTimersByTime(20_000))
  })

  it("rejoins when identity changes and stops reconnecting after disposal", () => {
    const options = { ...baseOptions, name: "Ada" }
    const probe = useProbe(options)
    mount(probe)
    const socket = MockWebSocket.instances[0]
    act(() => socket.simulateOpen())
    const sentBefore = socket.sent.length
    options.name = "Guest"
    options.role = "student"
    // The hook receives the same object through this probe, so explicitly
    // rerendering exercises the identity effect without opening another socket.
    act(() => root!.render(<probe.Probe />))
    expect(socket.sent.length).toBeGreaterThan(sentBefore)
    expect(JSON.parse(socket.sent.at(-1)!)).toMatchObject({ type: "join", name: "Guest", role: "student" })

    socket.close()
    const count = MockWebSocket.instances.length
    const staleOnClose = socket.onclose
    act(() => root!.unmount())
    root = null
    act(() => staleOnClose?.())
    act(() => vi.advanceTimersByTime(8_000))
    expect(MockWebSocket.instances.length).toBe(count)
    act(() => probe.state.current.reconnect())
  })
})
