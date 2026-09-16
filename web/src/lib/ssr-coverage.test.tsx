import React from "react"
import { renderToString } from "react-dom/server"
import { describe, expect, it, vi } from "vitest"

describe("external stores remain safe during SSR", () => {
  it("parses persisted auth and exposes stable server snapshots", async () => {
    localStorage.setItem(
      "slate.auth",
      JSON.stringify({
        user: { user_id: "teacher-1", email: "teacher@example.com", name: "Teacher", role: "teacher" },
        accessToken: "access",
        refreshToken: "refresh",
      }),
    )
    vi.resetModules()
    const auth = await import("./auth")
    expect(auth.getAuth()).toEqual({
      user: { user_id: "teacher-1", email: "teacher@example.com", name: "Teacher", role: "teacher" },
      accessToken: "access",
      refreshToken: "refresh",
    })
    expect(auth.getAccessToken()).toBe("access")
    expect(auth.getRefreshToken()).toBe("refresh")
    expect(renderToString(React.createElement(() => React.createElement("span", null, auth.useAuth().user?.name)))).toContain("<span")

    const blockedSetItem = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new Error("storage blocked")
    })
    expect(() => auth.setAuth({ user: null, accessToken: null, refreshToken: null })).not.toThrow()
    blockedSetItem.mockRestore()
    const blockedRemoveItem = vi.spyOn(window.localStorage, "removeItem").mockImplementation(() => {
      throw new Error("storage blocked")
    })
    expect(() => auth.clearAuth()).not.toThrow()
    blockedRemoveItem.mockRestore()

    localStorage.setItem("slate.auth", "42")
    vi.resetModules()
    const primitive = await import("./auth")
    expect(primitive.getAuth()).toEqual({ user: null, accessToken: null, refreshToken: null })
    localStorage.setItem("slate.auth", "{broken")
    vi.resetModules()
    const corrupt = await import("./auth")
    expect(corrupt.getAuth()).toEqual({ user: null, accessToken: null, refreshToken: null })
  })

  it("uses empty server snapshots for browser-backed stores", async () => {
    vi.resetModules()
    const classrooms = await import("./classrooms")
    const history = await import("./board-history")
    const bank = await import("./question-bank")
    const drafts = await import("./quiz-drafts")
    const rooms = await import("./recent-rooms")

    const HistoryProbe = () => React.createElement("span", null, JSON.stringify(history.useBoardHistory()))
    const BankProbe = () => React.createElement("span", null, JSON.stringify(bank.useQuestionBank()))
    const DraftProbe = () => React.createElement("span", null, JSON.stringify(drafts.useQuizDrafts()))
    const RoomsProbe = () => React.createElement("span", null, JSON.stringify(rooms.useRecentRooms()))
    const ClassroomProbe = () => React.createElement(
      "span",
      null,
      JSON.stringify({ all: classrooms.useClassrooms(), selected: classrooms.useSelectedClassroom() }),
    )
    expect(renderToString(React.createElement(HistoryProbe))).toContain("[]")
    expect(renderToString(React.createElement(BankProbe))).toContain("[]")
    expect(renderToString(React.createElement(DraftProbe))).toContain("[]")
    expect(renderToString(React.createElement(RoomsProbe))).toContain("[]")
    expect(renderToString(React.createElement(ClassroomProbe))).toContain("&quot;all&quot;:[]")
  })

  it("constructs the application router", async () => {
    vi.resetModules()
    const { getRouter } = await import("../router")
    expect(getRouter()).toBeTruthy()
  })
})
