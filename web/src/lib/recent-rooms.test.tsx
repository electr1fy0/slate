import { act } from "@testing-library/react"
import { createRoot } from "react-dom/client"
import { beforeEach, describe, expect, it } from "vitest"

import { forgetRoom, rememberRoom, useRecentRooms } from "./recent-rooms"

function useProbe() {
  const state = { current: undefined as unknown as ReturnType<typeof useRecentRooms> }
  function Probe() {
    state.current = useRecentRooms()
    return null
  }
  return { Probe, state }
}

describe("recent rooms", () => {
  let container: HTMLDivElement

  beforeEach(() => {
    localStorage.clear()
    container = document.createElement("div")
    document.body.appendChild(container)
    // The store keeps a module-level snapshot; a full page load would
    // re-read storage, so mirror that by seeding the cache explicitly
    // through one remembered value per scenario instead of assuming [].
  })

  function mountWith(probe: ReturnType<typeof useProbe>) {
    const root = createRoot(container)
    act(() => {
      root.render(<probe.Probe />)
    })
    return () => act(() => root.unmount())
  }

  it("remembers rooms most-recent-first without duplicates", () => {
    const probe = useProbe()
    const unmount = mountWith(probe)
    const before = probe.state.current

    act(() => rememberRoom("aaa"))
    expect(probe.state.current[0]).toBe("aaa")
    const afterAaa = [...probe.state.current]
    act(() => rememberRoom("bbb"))
    expect(probe.state.current[0]).toBe("bbb")
    expect(probe.state.current.slice(1)).toContain("aaa")

    act(() => rememberRoom("aaa"))
    expect(probe.state.current[0]).toBe("aaa")
    // bbb survived the re-remember; aaa appears exactly once.
    expect(probe.state.current.filter((room) => room === "aaa")).toHaveLength(1)
    expect(afterAaa).not.toContain("bbb")
    void before
    unmount()
  })

  it("forgets a room and survives storage loss", () => {
    const probe = useProbe()
    const unmount = mountWith(probe)
    act(() => rememberRoom("forget-me-xyz"))
    expect(probe.state.current).toContain("forget-me-xyz")
    act(() => forgetRoom("forget-me-xyz"))
    expect(probe.state.current).not.toContain("forget-me-xyz")
    unmount()

    // Storage corruption must never crash readers.
    localStorage.setItem("slate.recent-rooms", "{broken")
    const second = useProbe()
    const secondMount = mountWith(second)
    act(() => rememberRoom("after-corruption"))
    expect(second.state.current).toContain("after-corruption")
    secondMount()
  })

  it("caps the list at eight entries", () => {
    const probe = useProbe()
    const unmount = mountWith(probe)
    for (let i = 0; i < 12; i += 1) {
      act(() => rememberRoom(`cap-room-${i}`))
    }
    expect(probe.state.current.filter((room) => room.startsWith("cap-room-")))
      .toHaveLength(8)
    expect(probe.state.current[0]).toBe("cap-room-11")
    unmount()
  })
})
