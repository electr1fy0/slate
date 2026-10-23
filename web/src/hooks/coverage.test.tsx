import { act, renderHook } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { useIsMobile } from "./use-mobile"
import { useMounted } from "./use-mounted"
import { useOnlineStatus } from "./use-online-status"

describe("browser state hooks", () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("tracks the mobile media query and removes its listener", () => {
    let change: (() => void) | undefined
    const removeChange = vi.fn()
    const addChange = vi.fn((_event: string, listener: () => void) => {
      change = listener
    })
    vi.spyOn(window, "matchMedia").mockReturnValue({
      get matches() {
        return window.innerWidth < 768
      },
      media: "(max-width: 767px)",
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: addChange,
      removeEventListener: removeChange,
      dispatchEvent: vi.fn(),
    })
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 500,
      writable: true,
    })

    const hook = renderHook(() => useIsMobile())
    expect(hook.result.current).toBe(true)
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 1000,
      writable: true,
    })
    act(() => change?.())
    expect(hook.result.current).toBe(false)
    hook.unmount()
    expect(addChange).toHaveBeenCalledOnce()
    expect(removeChange).toHaveBeenCalledWith("change", change)
  })

  it("reports mounted after the first effect", () => {
    const hook = renderHook(() => useMounted())
    expect(hook.result.current).toBe(true)
  })

  it("follows online and offline browser events", () => {
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: true,
    })
    const hook = renderHook(() => useOnlineStatus())
    expect(hook.result.current).toBe(true)
    act(() => window.dispatchEvent(new Event("offline")))
    expect(hook.result.current).toBe(false)
    act(() => window.dispatchEvent(new Event("online")))
    expect(hook.result.current).toBe(true)
    hook.unmount()
    act(() => window.dispatchEvent(new Event("offline")))
    expect(hook.result.current).toBe(true)
  })

  it("starts offline when the browser reports no network", () => {
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    })
    const hook = renderHook(() => useOnlineStatus())
    expect(hook.result.current).toBe(false)
    hook.unmount()
  })

  it("defaults to online for server-like environments without navigator", () => {
    vi.stubGlobal("navigator", undefined)
    const hook = renderHook(() => useOnlineStatus())
    expect(hook.result.current).toBe(true)
    hook.unmount()
    vi.unstubAllGlobals()
  })
})
