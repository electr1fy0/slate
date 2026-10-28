import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { boardMessageBatches, validBoardMessage } from "@/hooks/use-board-sync"
import { mergeBoardElements } from "./board-elements"

beforeEach(() => {
  localStorage.clear()
  vi.resetModules()
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe("board transport boundaries", () => {
  it.each([
    null,
    [],
    { type: "init", you: "me", elements: [], peers: null },
    { type: "elements", elements: [null] },
    { type: "permissions", students_draw: "yes" },
    { type: "cursor", peer: {} },
    { type: "error", message: {} },
  ])("ignores malformed server data %#", (value) => {
    expect(validBoardMessage(value)).toBe(false)
  })
  it("accepts the authenticated room envelope", () => {
    expect(
      validBoardMessage({
        type: "init",
        you: "me",
        elements: [{ id: "shape", version: 1 }],
        peers: [{ id: "p", name: "Peer", role: "teacher", cursor: null }],
        room: { students_draw: false },
      })
    ).toBe(true)
  })
  it("splits large UTF-8 scenes without losing or reordering elements", () => {
    const elements = Array.from({ length: 10 }, (_, index) => ({
      id: String(index),
      version: 1,
      text: "🙂".repeat(3000),
    }))
    const batches = boardMessageBatches({ type: "elements", elements })
    expect(batches.length).toBeGreaterThan(1)
    expect(
      batches.flatMap((message) =>
        message.type === "elements" ? message.elements : []
      )
    ).toEqual(elements)
    for (const batch of batches)
      expect(
        new TextEncoder().encode(JSON.stringify(batch)).length
      ).toBeLessThan(65536)
    expect(() =>
      boardMessageBatches({
        type: "elements",
        elements: [{ id: "oversize", text: "x".repeat(65536) }],
      })
    ).toThrow(/too large/)
  })
  it("recovers valid cache rows and resolves duplicate revisions deterministically", () => {
    const invalid = [null, { id: 1 }, { id: "bad", version: -1 }]
    const rows = mergeBoardElements([], [
      ...invalid,
      { id: "shape", version: 5, versionNonce: 9 },
      { id: "shape", version: 3 },
      { id: "shape", version: 5, versionNonce: 2 },
      { id: "other" },
    ] as never)
    expect(rows).toEqual([
      { id: "shape", version: 5, versionNonce: 2 },
      { id: "other" },
    ])
    expect(
      mergeBoardElements(rows, [{ id: "delta", version: 1 }])
    ).toHaveLength(3)
  })
})

describe("shared admin token", () => {
  it("updates both mounted consumers in the same tab", async () => {
    const admin = await import("./admin-token")
    const first = renderHook(admin.useAdminToken)
    const second = renderHook(admin.useAdminToken)
    act(() => first.result.current[1]("new-token"))
    expect(second.result.current[0]).toBe("new-token")
  })
  it("responds to cross-tab changes and clear while ignoring session storage", async () => {
    const admin = await import("./admin-token")
    const view = renderHook(admin.useAdminToken)
    act(() => admin.storeAdminToken("old"))
    localStorage.setItem("slate.admin-token", "new")
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "slate.admin-token",
          storageArea: localStorage,
        })
      )
    )
    expect(view.result.current[0]).toBe("new")
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "slate.admin-token",
          storageArea: sessionStorage,
        })
      )
    )
    expect(view.result.current[0]).toBe("new")
    localStorage.clear()
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", { key: null, storageArea: localStorage })
      )
    )
    expect(view.result.current[0]).toBe("")
  })
  it("retains one shared ephemeral token when storage is blocked", async () => {
    const admin = await import("./admin-token")
    const first = renderHook(admin.useAdminToken)
    const second = renderHook(admin.useAdminToken)
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked")
    })
    act(() => first.result.current[1]("ephemeral"))
    expect(second.result.current[0]).toBe("ephemeral")
    expect(admin.readAdminToken()).toBe("ephemeral")
  })
})
