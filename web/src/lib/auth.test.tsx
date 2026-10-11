import { beforeEach, describe, expect, it } from "vitest"
import { act } from "@testing-library/react"
import { createRoot } from "react-dom/client"

import {
  clearAuth,
  getAccessToken,
  getAuth,
  isAuthenticated,
  setAuth,
  useAuth,
} from "./auth"

const SESSION = {
  user: { user_id: "u1", email: "a@b.com", name: "Ada", role: "student" as const },
  accessToken: "access-1",
  refreshToken: "refresh-1",
}

function renderHook() {
  const state = { current: undefined as unknown as ReturnType<typeof useAuth> }
  function Probe() {
    state.current = useAuth()
    return null
  }
  return { Probe }
}

describe("auth store", () => {
  let container: HTMLDivElement

  beforeEach(() => {
    localStorage.clear()
    container = document.createElement("div")
    document.body.appendChild(container)
    return () => {
      container.remove()
    }
  })

  function mount() {
    const { Probe } = renderHook()
    const root = createRoot(container)
    act(() => {
      root.render(<Probe />)
    })
    return () => act(() => root.unmount())
  }

  it("starts empty and unauthenticated", () => {
    const unmount = mount()
    expect(getAuth().user).toBeNull()
    expect(getAccessToken()).toBeNull()
    expect(isAuthenticated()).toBe(false)
    unmount()
  })

  it("setAuth persists and notifies subscribers", () => {
    const unmount = mount()
    expect(isAuthenticated()).toBe(false)
    act(() => setAuth(SESSION))
    expect(isAuthenticated()).toBe(true)
    expect(JSON.parse(localStorage.getItem("slate.auth")!)).toMatchObject({
      accessToken: "access-1",
    })
    unmount()
  })

  it("clearAuth wipes storage and state including legacy keys", () => {
    localStorage.setItem("slate.session", JSON.stringify({ role: "student" }))
    const unmount = mount()
    act(() => setAuth(SESSION))
    expect(isAuthenticated()).toBe(true)
    act(() => clearAuth())
    expect(localStorage.getItem("slate.auth")).toBeNull()
    expect(localStorage.getItem("slate.session")).toBeNull()
    expect(isAuthenticated()).toBe(false)
    unmount()
  })

  it("updates the active identity when another browser tab signs out or changes profile", () => {
    const unmount = mount()
    act(() => setAuth(SESSION))
    act(() => {
      localStorage.setItem("slate.auth", JSON.stringify({ ...SESSION, user: { ...SESSION.user, name: "Updated" } }))
      window.dispatchEvent(new StorageEvent("storage", { key: "slate.auth" }))
    })
    expect(getAuth().user?.name).toBe("Updated")
    act(() => {
      localStorage.removeItem("slate.auth")
      window.dispatchEvent(new StorageEvent("storage", { key: "slate.auth" }))
    })
    expect(isAuthenticated()).toBe(false)
    unmount()
  })

  it("recovers from corrupt persisted payloads", () => {
    localStorage.setItem("slate.auth", "{not json")
    const unmount = mount()
    expect(isAuthenticated()).toBe(false)
    unmount()

    localStorage.setItem(
      "slate.auth",
      JSON.stringify({ user: { nope: true }, accessToken: 42 }),
    )
    const second = mount()
    const auth = getAuth()
    expect(auth.user).toBeNull()
    expect(auth.accessToken).toBeNull()
    second()
  })
})
