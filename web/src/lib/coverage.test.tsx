import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { QuestionDraft } from "./types"

const classroomRecord = {
  id: "remote-class",
  owner_id: "teacher-1",
  name: "Remote class",
  description: "Networks",
  join_code: "REMOTE",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  member_count: 1,
}

const draftQuestion: QuestionDraft = {
  question: "What is two plus two?",
  options: ["3", "4"],
  answer: 1,
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.doUnmock("./api")
  vi.doUnmock("./auth")
})

describe("small frontend libraries", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("handles admin tokens and its React binding", async () => {
    vi.resetModules()
    const admin = await import("./admin-token")
    expect(admin.readAdminToken()).toBe("")
    admin.storeAdminToken("secret")
    expect(admin.readAdminToken()).toBe("secret")
    admin.storeAdminToken("")
    expect(admin.readAdminToken()).toBe("")

    const hook = renderHook(() => admin.useAdminToken())
    expect(hook.result.current[0]).toBe("")
    act(() => hook.result.current[1]("next-secret"))
    expect(hook.result.current[0]).toBe("next-secret")
    expect(admin.readAdminToken()).toBe("next-secret")

    vi.spyOn(window.localStorage, "getItem").mockImplementation(() => {
      throw new Error("blocked")
    })
    expect(admin.readAdminToken()).toBe("next-secret")
    vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new Error("blocked")
    })
    expect(() => admin.storeAdminToken("ephemeral")).not.toThrow()
  })

  it("covers board history persistence, merging, CSV export, and download", async () => {
    const valid = Array.from({ length: 22 }, (_, index) => ({
      room_id: `room-${index}`,
      first_opened_at: `2026-01-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
      last_opened_at: `2026-01-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
      visits: index + 1,
      source: "local-history",
    }))
    localStorage.setItem(
      "slate.board-history",
      JSON.stringify([...valid, { nope: true }, null])
    )
    vi.resetModules()
    const history = await import("./board-history")
    const hook = renderHook(() => history.useBoardHistory())
    expect(hook.result.current).toHaveLength(20)

    act(() => history.recordBoardVisit("room-3"))
    expect(hook.result.current[0]?.room_id).toBe("room-3")
    expect(hook.result.current[0]?.visits).toBe(5)
    act(() => history.recordBoardVisit("brand-new"))
    expect(hook.result.current[0]?.room_id).toBe("brand-new")
    act(() => history.forgetBoardHistory("room-1"))
    expect(history.useBoardHistory).toBeTypeOf("function")

    const merged = history.mergeBoardHistory(
      ["missing", "room-2"],
      hook.result.current
    )
    expect(merged.some((entry) => entry.room_id === "missing")).toBe(true)
    expect(merged[0]?.last_opened_at >= merged.at(-1)!.last_opened_at).toBe(
      true
    )
    const csv = history.boardHistoryCsv([
      {
        room_id: 'room,"quoted"',
        first_opened_at: "first",
        last_opened_at: "last",
        visits: 3,
        source: "local-history",
      },
    ])
    expect(csv).toContain('"room,""quoted"""')

    vi.useFakeTimers()
    const anchor = document.createElement("a")
    const click = vi.spyOn(anchor, "click").mockImplementation(() => {})
    const createElement = vi
      .spyOn(document, "createElement")
      .mockReturnValue(anchor)
    const createObjectURL = vi.fn().mockReturnValue("blob:test")
    const revokeObjectURL = vi.fn()
    const urlConstructor = globalThis.URL
    Object.defineProperty(urlConstructor, "createObjectURL", {
      value: createObjectURL,
      configurable: true,
    })
    Object.defineProperty(urlConstructor, "revokeObjectURL", {
      value: revokeObjectURL,
      configurable: true,
    })
    history.downloadTextFile("history.csv", "body", "text/csv")
    expect(createObjectURL).toHaveBeenCalledOnce()
    expect(anchor.download).toBe("history.csv")
    expect(anchor.href).toBe("blob:test")
    expect(click).toHaveBeenCalledOnce()
    expect(revokeObjectURL).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1000)
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:test")
    vi.useRealTimers()
    createElement.mockRestore()

    localStorage.setItem(
      "slate.board-history",
      JSON.stringify({ invalid: true })
    )
    vi.resetModules()
    const nonArray = await import("./board-history")
    const nonArrayHook = renderHook(() => nonArray.useBoardHistory())
    expect(nonArrayHook.result.current).toEqual([])
    nonArrayHook.unmount()
    vi.resetModules()
    const blank = await import("./board-history")
    localStorage.setItem("slate.board-history", "{broken")
    expect(blank.mergeBoardHistory([], [])).toEqual([])
    act(() => blank.recordBoardVisit("after-reset"))
    vi.stubGlobal("window", undefined)
    vi.resetModules()
    const server = await import("./board-history")
    expect(() => server.recordBoardVisit("server-call")).not.toThrow()
  })

  it("validates quiz question-bank records and updates its external store", async () => {
    localStorage.setItem(
      "slate.question-bank.library-teacher",
      JSON.stringify([
        {
          id: "q1",
          saved_at: "2026-01-01T00:00:00Z",
          question: "ok",
          options: ["a", "b"],
          answer: 0,
        },
        { bad: true },
        null,
      ])
    )
    vi.resetModules()
    const auth = await import("./auth")
    auth.setAuth({
      user: {
        user_id: "library-teacher",
        name: "Teacher",
        email: "t@example.test",
        role: "teacher",
      },
      accessToken: "a",
      refreshToken: "r",
    })
    const bank = await import("./question-bank")
    const hook = renderHook(() => bank.useQuestionBank())
    expect(hook.result.current).toHaveLength(1)
    let saved: Awaited<ReturnType<typeof bank.saveQuestionToBank>>
    act(() => {
      saved = bank.saveQuestionToBank({
        ...draftQuestion,
        options: [...draftQuestion.options],
      })
    })
    const savedItem = saved!
    expect(savedItem.id).toMatch(/^question-/)
    expect(hook.result.current[0]?.id).toBe(savedItem.id)
    expect(savedItem.options).not.toBe(draftQuestion.options)
    act(() => bank.removeQuestionFromBank(savedItem.id))
    expect(hook.result.current.some((item) => item.id === savedItem.id)).toBe(
      false
    )

    hook.unmount()
    vi.stubGlobal("window", undefined)
    expect(() => bank.saveQuestionToBank(draftQuestion)).not.toThrow()
    vi.unstubAllGlobals()

    vi.resetModules()
    localStorage.setItem(
      "slate.question-bank.library-teacher",
      JSON.stringify({ invalid: true })
    )
    const nonArray = await import("./question-bank")
    expect(renderHook(() => nonArray.useQuestionBank()).result.current).toEqual(
      []
    )
    vi.resetModules()
    localStorage.setItem("slate.question-bank.library-teacher", "not-json")
    const corrupt = await import("./question-bank")
    expect(renderHook(() => corrupt.useQuestionBank()).result.current).toEqual(
      []
    )

    vi.stubGlobal("window", undefined)
    vi.resetModules()
    const server = await import("./question-bank")
    expect(() => server.saveQuestionToBank(draftQuestion)).not.toThrow()
  })

  it("validates, replaces, and removes saved quiz drafts", async () => {
    localStorage.setItem(
      "slate.quiz-drafts.library-teacher",
      JSON.stringify([
        {
          draft_id: "d1",
          title: "Existing",
          updated_at: "2026-01-01T00:00:00Z",
          questions: [],
        },
        { bad: true },
        null,
      ])
    )
    vi.resetModules()
    const auth = await import("./auth")
    auth.setAuth({
      user: {
        user_id: "library-teacher",
        name: "Teacher",
        email: "t@example.test",
        role: "teacher",
      },
      accessToken: "a",
      refreshToken: "r",
    })
    const drafts = await import("./quiz-drafts")
    const hook = renderHook(() => drafts.useQuizDrafts())
    expect(hook.result.current).toHaveLength(1)
    let first: Awaited<ReturnType<typeof drafts.saveQuizDraft>>
    act(() => {
      first = drafts.saveQuizDraft(
        { title: "New", questions: [draftQuestion] },
        "d2"
      )
    })
    const firstDraft = first!
    expect(firstDraft).toMatchObject({ draft_id: "d2", title: "New" })
    expect(firstDraft.questions[0]?.options).not.toBe(draftQuestion.options)
    let replacement: Awaited<ReturnType<typeof drafts.saveQuizDraft>>
    act(() => {
      replacement = drafts.saveQuizDraft(
        { title: "Updated", questions: [] },
        "d2"
      )
    })
    const replacementDraft = replacement!
    expect(replacementDraft.title).toBe("Updated")
    expect(
      hook.result.current.filter((draft) => draft.draft_id === "d2")
    ).toHaveLength(1)
    act(() => drafts.removeQuizDraft("d2"))
    expect(hook.result.current.some((draft) => draft.draft_id === "d2")).toBe(
      false
    )

    hook.unmount()
    vi.stubGlobal("window", undefined)
    expect(() =>
      drafts.saveQuizDraft({ title: "Ephemeral", questions: [] })
    ).not.toThrow()
    vi.unstubAllGlobals()

    vi.resetModules()
    localStorage.setItem(
      "slate.quiz-drafts.library-teacher",
      JSON.stringify({ invalid: true })
    )
    const nonArray = await import("./quiz-drafts")
    expect(renderHook(() => nonArray.useQuizDrafts()).result.current).toEqual(
      []
    )
    localStorage.setItem("slate.quiz-drafts.library-teacher", "{broken")
    vi.resetModules()
    const corrupt = await import("./quiz-drafts")
    expect(renderHook(() => corrupt.useQuizDrafts()).result.current).toEqual([])
    vi.stubGlobal("window", undefined)
    vi.resetModules()
    const server = await import("./quiz-drafts")
    expect(() =>
      server.saveQuizDraft({ title: "SSR", questions: [] })
    ).not.toThrow()
  })

  it("covers recent rooms, exam state, class names, and query defaults", async () => {
    localStorage.setItem("slate.recent-rooms", JSON.stringify(["a", 2, "b"]))
    vi.resetModules()
    const rooms = await import("./recent-rooms")
    const hook = renderHook(() => rooms.useRecentRooms())
    expect(hook.result.current).toEqual(["a", "b"])
    act(() => rooms.rememberRoom("c"))
    act(() => rooms.forgetRoom("a"))
    expect(hook.result.current).toEqual(["c", "b"])
    const blockedSetItem = vi
      .spyOn(window.localStorage, "setItem")
      .mockImplementation(() => {
        throw new Error("blocked")
      })
    expect(() => rooms.rememberRoom("ephemeral")).not.toThrow()
    expect(() => rooms.forgetRoom("ephemeral")).not.toThrow()
    hook.unmount()
    blockedSetItem.mockRestore()

    localStorage.setItem("slate.recent-rooms", JSON.stringify({ bad: true }))
    vi.resetModules()
    const nonArray = await import("./recent-rooms")
    expect(renderHook(() => nonArray.useRecentRooms()).result.current).toEqual(
      []
    )
    localStorage.setItem("slate.recent-rooms", "{broken")
    vi.resetModules()
    const broken = await import("./recent-rooms")
    expect(renderHook(() => broken.useRecentRooms()).result.current).toEqual([])

    vi.resetModules()
    const types = await import("./types")
    const before = Date.parse("2026-01-01T00:00:00Z")
    expect(types.examState({ opens_at: "2026-01-01T00:00:01Z" }, before)).toBe(
      "upcoming"
    )
    expect(
      types.examState({ closes_at: "2026-01-01T00:00:00Z" }, before + 1)
    ).toBe("closed")
    expect(types.examState({}, before)).toBe("open")

    const utils = await import("./utils")
    expect(utils.cn("px-2", "px-4", false, null)).toBe("px-4")
    const client = (await import("./query-client")).makeQueryClient()
    expect(client.getDefaultOptions().queries).toMatchObject({
      staleTime: 5000,
      retry: 1,
      refetchOnWindowFocus: false,
    })
  })

  it("enforces auth and teacher redirects", async () => {
    localStorage.setItem(
      "slate.auth",
      JSON.stringify({ accessToken: "only-access" })
    )
    vi.resetModules()
    const noUserAuth = await import("./auth")
    expect(noUserAuth.getAuth().user).toBeNull()

    localStorage.setItem(
      "slate.auth",
      JSON.stringify({
        user: { email: null, name: null, role: "student" },
        accessToken: 42,
        refreshToken: {},
      })
    )
    vi.resetModules()
    const invalidUserAuth = await import("./auth")
    expect(invalidUserAuth.getAuth().user).toBeNull()

    localStorage.setItem(
      "slate.auth",
      JSON.stringify({
        user: { user_id: "u", email: null, name: null, role: "student" },
        accessToken: 42,
        refreshToken: {},
      })
    )
    vi.resetModules()
    const partialAuth = await import("./auth")
    expect(partialAuth.getAuth()).toMatchObject({
      user: null,
      accessToken: null,
      refreshToken: null,
    })

    localStorage.removeItem("slate.auth")
    vi.resetModules()
    const auth = await import("./auth")
    const guard = await import("./guard")
    expect(() => guard.requireAuth({ href: "/private" })).toThrow()
    auth.setAuth({
      user: {
        user_id: "s",
        email: "s@example.com",
        name: "Sam",
        role: "student",
      },
      accessToken: "a",
      refreshToken: "r",
    })
    expect(() => guard.requireAuth({ href: "/private" })).not.toThrow()
    expect(() => guard.requireTeacher({ href: "/teacher" })).toThrow()
    auth.setAuth({
      user: {
        user_id: "t",
        email: "t@example.com",
        name: "Teacher",
        role: "teacher",
      },
      accessToken: "a",
      refreshToken: "r",
    })
    expect(() => guard.requireTeacher({ href: "/teacher" })).not.toThrow()
  })
})

describe("classroom store", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  async function freshClassrooms(api?: Record<string, unknown>) {
    vi.resetModules()
    if (api) vi.doMock("./api", () => ({ api }))
    return import("./classrooms")
  }

  it("manages local class membership and selection edge cases", async () => {
    const classrooms = await freshClassrooms()
    expect(classrooms.getClassroom("missing")).toBeNull()
    const created = classrooms.createClassroom({
      name: "  Systems 101 ",
      owner: { id: "teacher", name: "Ada" },
    })
    expect(created.subject).toBe("General studies")
    expect(
      classrooms.addClassroomMember("missing", { name: "Nobody" })
    ).toBeNull()
    const invited = classrooms.addClassroomMember(created.id, {
      name: "Lin",
      email: "LIN@example.com",
      status: "invited",
    })!
    expect(
      classrooms.addClassroomMember(created.id, {
        name: "Other",
        email: " lin@example.com",
      })?.members
    ).toHaveLength(2)
    expect(
      classrooms.addClassroomMember(created.id, { name: "Lin" })?.members
    ).toHaveLength(2)
    expect(classrooms.joinClassroom("wrong", { id: "x", name: "X" })).toBeNull()
    expect(classrooms.joinClassroom("", { id: "x", name: "X" })).toBeNull()
    created.join_code = "ABC234"
    classrooms.getClassroom(created.id)!.join_code = "ABC234"
    expect(
      classrooms.joinClassroom(created.join_code.toLowerCase(), {
        id: invited.members[1].id,
        name: "Lin",
      })?.members[1]?.status
    ).toBe("active")
    expect(
      classrooms.joinClassroom(created.join_code, {
        id: "new",
        name: "New",
        email: "new@example.com",
      })?.members
    ).toHaveLength(3)
    expect(
      classrooms.removeClassroomMember(created.id, created.members[0].id)
        ?.members
    ).toHaveLength(3)
    expect(classrooms.removeClassroomMember("missing", "x")).toBeNull()
    expect(
      classrooms.removeClassroomMember(created.id, "new")?.members
    ).toHaveLength(2)
    expect(
      classrooms.removeClassroomMember(created.id, "new")?.members
    ).toHaveLength(2)
    classrooms.setClassroomActiveRoom(created.id, "room-1")
    classrooms.setClassroomActiveRoom(created.id, undefined)
    classrooms.selectClassroom("missing")
    classrooms.selectClassroom(null)
    expect(classrooms.getClassroom(created.id)).not.toBeNull()
    expect(classrooms.newClassJoinCode()).toHaveLength(6)

    const hook = renderHook(() => ({
      all: classrooms.useClassrooms(),
      selected: classrooms.useSelectedClassroom(),
    }))
    expect(hook.result.current.all.length).toBeGreaterThan(0)
    expect(hook.result.current.selected).toBeTruthy()
    act(() => classrooms.selectClassroom(created.id))
    expect(hook.result.current.selected?.id).toBe(created.id)

    vi.stubGlobal("crypto", undefined)
    expect(classrooms.newClassJoinCode()).toHaveLength(6)
    const fallback = classrooms.createClassroom({
      name: "Fallback",
      owner: { name: "Owner" },
    })
    classrooms.selectClassroom(null)
    classrooms.setClassroomActiveRoom(created.id, "room-after-fallback")
    expect(fallback.id).toMatch(/^class-/)
  })

  it("syncs local classes, roster, and server mutations including failures", async () => {
    const listClassrooms = vi.fn().mockResolvedValue([classroomRecord])
    const createClassroom = vi
      .fn()
      .mockResolvedValue({ ...classroomRecord, id: "created-remote" })
    const joinClassroom = vi.fn().mockResolvedValue(classroomRecord)
    const classroomRoster = vi.fn().mockResolvedValue([
      {
        classroom_id: "remote-class",
        user_id: "student-1",
        name: "Student",
        email: "student@example.com",
        role: "student",
        joined_at: "2026-01-01T00:00:00Z",
      },
      {
        classroom_id: "remote-class",
        user_id: "teacher-2",
        name: "Other teacher",
        email: "teacher2@example.com",
        role: "teacher",
        joined_at: "2026-01-01T00:00:00Z",
      },
    ])
    const removeClassroomMember = vi.fn().mockResolvedValue(undefined)
    const classrooms = await freshClassrooms({
      listClassrooms,
      createClassroom,
      joinClassroom,
      classroomRoster,
      removeClassroomMember,
    })
    const local = classrooms.createClassroom({
      name: "Local",
      subject: "Networks",
      owner: { id: "teacher-1", name: "Owner" },
    })
    await classrooms.syncClassroomsFromApi()
    expect(classrooms.getClassroom("remote-class")?.name).toBe("Remote class")
    expect(classrooms.getClassroom(local.id)).not.toBeNull()
    listClassrooms.mockResolvedValueOnce([
      {
        ...classroomRecord,
        id: local.id,
        description: "",
        join_code: "",
        created_at: "",
      },
      {
        ...classroomRecord,
        id: "fallback-class",
        description: "",
        join_code: "",
        created_at: "",
      },
    ])
    await classrooms.syncClassroomsFromApi()
    expect(classrooms.getClassroom(local.id)?.subject).toBe("")
    await classrooms.syncClassroomRosterFromApi("fallback-class")
    const synced = await classrooms.createClassroomOnServer(local)
    expect(synced?.id).toBe("created-remote")
    expect(
      await classrooms.syncClassroomRosterFromApi("does-not-exist")
    ).toBeNull()
    expect(
      await classrooms.syncClassroomRosterFromApi("created-remote")
    ).toBeTruthy()
    expect(
      await classrooms.removeClassroomMemberOnServer(
        "created-remote",
        "student-1"
      )
    ).toBe(true)

    const joined = await classrooms.joinClassroomOnServer("REMOTE", {
      id: "student-1",
      name: "Student",
      email: " STUDENT@example.com ",
    })
    expect(joined?.members.some((member) => member.id === "student-1")).toBe(
      true
    )
    const joinedAgain = await classrooms.joinClassroomOnServer("REMOTE", {
      id: "student-1",
      name: "Student",
    })
    expect(joinedAgain).toBeTruthy()
    const joinedByEmail = await classrooms.joinClassroomOnServer("REMOTE", {
      id: "different-id",
      name: "Student",
      email: "student@example.com",
    })
    expect(joinedByEmail).toBeTruthy()
    const joinedWithoutEmail = await classrooms.joinClassroomOnServer(
      "REMOTE",
      { id: "without-email", name: "No email" }
    )
    expect(joinedWithoutEmail).toBeTruthy()
    expect(
      classrooms.mergeJoinedMember([], {
        id: "unique-member",
        name: "Unique member",
        email: "",
        role: "student",
        status: "active",
        joined_at: "2026-01-01T00:00:00Z",
      })
    ).toHaveLength(1)
    const matchedMember = {
      id: "matched",
      name: "Matched",
      email: "matched@example.com",
      role: "student" as const,
      status: "invited" as const,
      joined_at: "2026-01-01T00:00:00Z",
    }
    const untouchedMember = {
      ...matchedMember,
      id: "untouched",
      email: "untouched@example.com",
    }
    expect(
      classrooms.mergeJoinedMember(
        [matchedMember, untouchedMember],
        { ...matchedMember, status: "active" },
        "matched@example.com"
      )
    ).toEqual([{ ...matchedMember, status: "active" }, untouchedMember])
    joinClassroom.mockResolvedValueOnce({
      ...classroomRecord,
      id: "brand-new-remote",
    })
    const joinedNewClass = await classrooms.joinClassroomOnServer("REMOTE", {
      id: "new-server-student",
      name: "New server student",
    })
    expect(joinedNewClass?.id).toBe("brand-new-remote")
    expect(classrooms.getClassroom("truly-new-remote")).toBeNull()
    joinClassroom.mockResolvedValueOnce({
      ...classroomRecord,
      id: "truly-new-remote",
    })
    expect(
      (
        await classrooms.joinClassroomOnServer("REMOTE", {
          id: "another-server-student",
          name: "Another server student",
        })
      )?.id
    ).toBe("truly-new-remote")
    await classrooms.createClassroomOnServer(local)

    listClassrooms.mockRejectedValueOnce(new Error("offline"))
    createClassroom.mockRejectedValueOnce(new Error("offline"))
    joinClassroom.mockRejectedValueOnce(new Error("offline"))
    classroomRoster.mockRejectedValueOnce(new Error("offline"))
    removeClassroomMember.mockRejectedValueOnce(new Error("offline"))
    await expect(classrooms.syncClassroomsFromApi()).resolves.toBe(false)
    expect(await classrooms.createClassroomOnServer(local)).toBeNull()
    expect(
      await classrooms.joinClassroomOnServer("REMOTE", { name: "Offline" })
    ).toBeNull()
    expect(
      await classrooms.syncClassroomRosterFromApi("remote-class")
    ).toBeNull()
    classroomRoster.mockResolvedValueOnce([
      {
        user_id: "x",
        name: "X",
        role: "student",
        email: "x@example.test",
        joined_at: "2026-01-01T00:00:00Z",
      },
    ])
    expect(
      await classrooms.removeClassroomMemberOnServer("remote-class", "x")
    ).toBe(false)
  })

  it("preserves server owners and handles storage failures", async () => {
    const api = {
      listClassrooms: vi.fn().mockResolvedValue([]),
      classroomRoster: vi.fn().mockResolvedValue([]),
      createClassroom: vi.fn().mockResolvedValue(classroomRecord),
      joinClassroom: vi.fn().mockResolvedValue(classroomRecord),
      removeClassroomMember: vi.fn().mockResolvedValue(undefined),
    }
    const classrooms = await freshClassrooms(api)
    const owner = classrooms.createClassroom({
      name: "Owner class",
      owner: { id: "teacher-1", name: "Owner", email: "owner@example.com" },
    })
    vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new Error("blocked")
    })
    expect(() =>
      classrooms.createClassroom({ name: "Storage", owner: { name: "x" } })
    ).not.toThrow()
    expect(owner.members[0]?.role).toBe("teacher")
    api.classroomRoster.mockResolvedValueOnce([])
    const synced = await classrooms.syncClassroomRosterFromApi(owner.id)
    expect(synced?.members[0]?.role).toBe("teacher")
  })

  it("filters malformed persisted classes and tolerates selected-storage failures", async () => {
    localStorage.setItem("slate.classrooms", "not-json")
    let classrooms = await freshClassrooms()
    expect(classrooms.getClassroom("anything")).toBeNull()

    localStorage.setItem(
      "slate.classrooms",
      JSON.stringify({ not: "an array" })
    )
    classrooms = await freshClassrooms()
    expect(classrooms.getClassroom("anything")).toBeNull()

    const valid = {
      id: "valid-class",
      name: "Valid class",
      subject: "Math",
      join_code: "ABC234",
      created_at: "2026-01-01T00:00:00Z",
      members: [
        {
          id: "student",
          name: "Student",
          email: "",
          role: "student",
          status: "active",
          joined_at: "2026-01-01T00:00:00Z",
        },
      ],
    }
    localStorage.setItem(
      "slate.classrooms",
      JSON.stringify([
        null,
        {},
        { ...valid, members: [{}] },
        {
          ...valid,
          members: [
            {
              id: "bad-joined",
              name: "Bad",
              email: "",
              role: "student",
              status: "active",
              joined_at: 42,
            },
          ],
        },
        { ...valid, members: [null] },
        valid,
        {
          ...valid,
          id: "invited-class",
          members: [
            {
              id: "invited",
              name: "Invited",
              email: "",
              role: "student",
              status: "invited",
              joined_at: "2026-01-01T00:00:00Z",
            },
          ],
        },
      ])
    )
    classrooms = await freshClassrooms()
    expect(classrooms.getClassroom("valid-class")?.name).toBe("Valid class")

    localStorage.setItem("slate.selected-classroom", "valid-class")
    const originalGetItem = window.localStorage.getItem.bind(
      window.localStorage
    )
    const getItem = vi
      .spyOn(window.localStorage, "getItem")
      .mockImplementation((key) => {
        if (key === "slate.selected-classroom") throw new Error("blocked")
        return originalGetItem(key)
      })
    classrooms = await freshClassrooms()
    const hook = renderHook(() => classrooms.useSelectedClassroom())
    expect(hook.result.current?.id).toBe("valid-class")
    getItem.mockRestore()
  })

  it("uses empty browser snapshots when the window is unavailable", async () => {
    vi.stubGlobal("window", undefined)
    const classrooms = await freshClassrooms()
    expect(classrooms.getClassroom("server-only")).toBeNull()
    expect(classrooms.getSelectedClassroomId()).toBeNull()
  })
})

describe("API client contract", () => {
  const session = {
    user: {
      user_id: "u1",
      email: "u@example.com",
      name: "User",
      role: "student" as const,
    },
    accessToken: "access",
    refreshToken: "refresh",
  }

  function response(status = 200, body: unknown = {}) {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: vi.fn().mockResolvedValue(body),
      text: vi.fn().mockResolvedValue(JSON.stringify(body)),
    } as unknown as Response
  }

  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it("serializes every public endpoint with the expected method and path", async () => {
    const { api } = await import("./api")
    const fetchMock = vi.fn().mockResolvedValue(response())
    vi.stubGlobal("fetch", fetchMock)
    const draft = { title: "Quiz", questions: [draftQuestion] }

    await api.register({ email: "u@example.com", password: "secret" })
    await api.login({ email: "u@example.com", password: "secret" })
    await api.listExams()
    await api.listClassrooms()
    await api.createClassroom({ name: "Class", description: "Subject" })
    await api.joinClassroom("A/B")
    await api.classroomRoster("class/id")
    await api.removeClassroomMember("class/id", "member/id")
    await api.boardHistory("room/id", 7)
    await api.boardExport("room/id")
    await api.boardRestore("room/id", 2)
    await api.getExam("exam/id")
    await api.previewExam("exam/id")
    await api.createExam(draft)
    await api.listQuizDrafts()
    await api.getQuizDraft("draft/id")
    await api.createQuizDraft(draft)
    await api.updateQuizDraft("draft/id", draft)
    await api.deleteQuizDraft("draft/id")
    await api.publishQuizDraft("draft/id")
    await api.duplicateQuiz({ source_id: "source", title: "Copy" })
    await api.duplicateQuizDraft("draft/id", { id: "copy" })
    await api.duplicateQuizDraft("draft/id")
    await api.getAttempt("exam/id")
    await api.saveAttempt("exam/id", {
      answers: { "0": 1 },
      expected_version: 0,
      request_id: "req",
    })
    await api.submitSavedAttempt("exam/id", "req")
    await api.submitAnswers("exam/id", { answers: { "0": 1 } })
    await api.myResult("exam/id")
    await api.patchExam("exam/id", { title: "Renamed" })
    await api.closeExam("exam/id")
    await api.deleteExam("exam/id")
    await api.examResults("exam/id")
    await api.submissions("admin", 3)
    await api.status("admin")
    await api.setChaos("admin", { error_pct: 5 })
    await api.resetChaos("admin")
    await api.promote("admin")

    const calls = fetchMock.mock.calls.map(([url, init]) => ({
      url: String(url),
      init: init as RequestInit,
    }))
    expect(
      calls.some((call) => call.url.endsWith("/classrooms/class%2Fid/roster"))
    ).toBe(true)
    expect(
      calls.some((call) =>
        call.url.endsWith("/classrooms/class%2Fid/members/member%2Fid")
      )
    ).toBe(true)
    expect(
      calls.find((call) => call.url.endsWith("/auth/register"))?.init.method
    ).toBe("POST")
    expect(
      calls.find((call) => call.url.endsWith("/admin/status"))?.init.headers
    ).toMatchObject({ "x-admin-token": "admin" })
    expect(calls.some((call) => call.init.method === "PUT")).toBe(true)
  })

  it("refreshes once, preserves custom auth, handles 204, and reports failures", async () => {
    const { api, ApiError } = await import("./api")
    const auth = await import("./auth")
    auth.setAuth(session)
    const fetchMock = vi.fn()
    fetchMock
      .mockResolvedValueOnce(response(401, { detail: "expired" }))
      .mockResolvedValueOnce(
        response(200, {
          access_token: "new-access",
          refresh_token: "new-refresh",
          user: session.user,
        })
      )
      .mockResolvedValueOnce(response(204))
    vi.stubGlobal("fetch", fetchMock)
    await api.deleteQuizDraft("draft")
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect((fetchMock.mock.calls[2][1] as RequestInit).headers).toMatchObject({
      Authorization: "Bearer new-access",
    })

    fetchMock.mockReset()
    fetchMock.mockResolvedValue(response(200, {}))
    await api.status("token")
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).toMatchObject({
      Authorization: "Bearer new-access",
      "x-admin-token": "token",
    })

    auth.clearAuth()
    fetchMock.mockReset()
    fetchMock.mockResolvedValue(response(401, { detail: "still unauthorized" }))
    const noRefresh = await api.listExams().catch((error: unknown) => error)
    expect(noRefresh).toBeInstanceOf(ApiError)
    expect((noRefresh as InstanceType<typeof ApiError>).message).toBe(
      "still unauthorized"
    )

    auth.setAuth(session)
    fetchMock.mockReset()
    fetchMock
      .mockResolvedValueOnce(
        response(401, { detail: { code: "BAD", message: "bad session" } })
      )
      .mockResolvedValueOnce(response(403, {}))
    const refreshRejected = await api
      .listExams()
      .catch((error: unknown) => error)
    expect(refreshRejected).toBeInstanceOf(ApiError)
    expect((refreshRejected as InstanceType<typeof ApiError>).status).toBe(401)

    auth.setAuth(session)
    fetchMock.mockReset()
    fetchMock
      .mockResolvedValueOnce(response(401))
      .mockRejectedValueOnce(new Error("refresh offline"))
    const refreshOffline = await api
      .listExams()
      .catch((error: unknown) => error)
    expect(refreshOffline).toBeInstanceOf(ApiError)

    fetchMock.mockReset()
    const badBody = response(500)
    ;(badBody.json as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error("not json")
    )
    fetchMock.mockResolvedValue(badBody)
    const generic = await api.listExams().catch((error: unknown) => error)
    expect(generic).toBeInstanceOf(ApiError)
    expect((generic as InstanceType<typeof ApiError>).message).toBe(
      "Request failed (500)"
    )

    fetchMock.mockResolvedValue(
      response(400, { detail: { message: "bad request", code: "BAD_REQUEST" } })
    )
    const coded = await api.listExams().catch((error: unknown) => error)
    expect(coded).toBeInstanceOf(ApiError)
    expect((coded as InstanceType<typeof ApiError>).message).toBe("bad request")
    expect((coded as InstanceType<typeof ApiError>).code).toBe("BAD_REQUEST")

    fetchMock.mockResolvedValue(
      response(400, { detail: { message: "uncoded request", code: 42 } })
    )
    const uncoded = await api.listExams().catch((error: unknown) => error)
    expect(uncoded).toBeInstanceOf(ApiError)
    expect((uncoded as InstanceType<typeof ApiError>).message).toBe(
      "uncoded request"
    )
    expect((uncoded as InstanceType<typeof ApiError>).code).toBeUndefined()
  })

  it("does not attempt a refresh when the session loses its refresh token", async () => {
    vi.doMock("./auth", () => ({
      clearAuth: vi.fn(),
      getAccessToken: vi.fn().mockReturnValue(null),
      getRefreshToken: vi
        .fn()
        .mockReturnValueOnce("refresh")
        .mockReturnValueOnce(null),
      setAuth: vi.fn(),
    }))
    const { api, ApiError } = await import("./api")
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(response(401, { detail: "unauthorized" }))
    )
    const error = await api.listExams().catch((value: unknown) => value)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as InstanceType<typeof ApiError>).status).toBe(401)
  })
})
