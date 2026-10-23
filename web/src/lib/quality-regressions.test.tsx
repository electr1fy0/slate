import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { answerMap, localId } from "./local-validation"
import { parseQuestionImport } from "./teacher-authoring"
import {
  filterAndSortAssessments,
  canRetakeAssessment,
  assessmentAttemptStatus,
} from "./assessment-history"
import {
  readQuizProgress,
  toggleFlaggedQuestion,
  visibleQuestionPositions,
  writeQuizProgress,
} from "./quiz-progress"
import type { ExamMeta } from "./types"

const session = {
  user: {
    user_id: "quality-user",
    name: "Teacher",
    email: "t@example.test",
    role: "teacher" as const,
  },
  accessToken: "access",
  refreshToken: "refresh",
}
const scope = {
  userId: "quality-user",
  examId: "quality-exam",
  attemptNumber: 1,
}
const question = { question: "Question?", options: ["One", "Two"], answer: 0 }
const date = "2026-01-01T00:00:00Z"

beforeEach(() => {
  localStorage.clear()
  vi.resetModules()
})
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe("restored identity contracts", () => {
  it.each([
    { ...session, user: { ...session.user, user_id: " " } },
    { ...session, user: { ...session.user, role: "administrator" } },
    { ...session, user: { ...session.user, email: {} } },
    { ...session, user: { ...session.user, name: [] } },
    { ...session, accessToken: null },
    { ...session, accessToken: " " },
  ])("rejects malformed identity %#", async (value) => {
    localStorage.setItem("slate.auth", JSON.stringify(value))
    const auth = await import("./auth")
    expect(auth.isAuthenticated()).toBe(false)
    expect(auth.getAccessToken()).toBeNull()
  })

  it("owns its snapshot and supports access-only sessions", async () => {
    const auth = await import("./auth")
    const source = structuredClone(session)
    auth.setAuth(source)
    source.user.name = "Mutated"
    expect(auth.getAuth().user?.name).toBe("Teacher")
    auth.setAuth({ ...session, refreshToken: null })
    expect(auth.isAuthenticated()).toBe(true)
  })

  it("ignores an unrelated storage area", async () => {
    const auth = await import("./auth")
    auth.setAuth(session)
    localStorage.removeItem("slate.auth")
    window.dispatchEvent(
      new StorageEvent("storage", {
        key: "slate.auth",
        storageArea: sessionStorage,
      })
    )
    expect(auth.isAuthenticated()).toBe(true)
  })
})

describe("answer recovery and retries", () => {
  it.each([
    [],
    [1],
    { "00": 0 },
    { "-1": 1 },
    { "0": -1 },
    { "0": 0.5 },
    { "0": Infinity },
    { "9007199254740993": 0 },
  ])("rejects malformed answer maps %#", (answers) => {
    expect(answerMap(answers)).toBe(false)
  })

  it("retains empty maps and valid canonical answers", () => {
    expect(answerMap({})).toBe(true)
    expect(answerMap({ "0": 1, "12": 0 })).toBe(true)
  })

  it.each([
    { attemptNumber: 0 },
    { attemptNumber: 0.5 },
    { requestId: " " },
    { createdAt: "invalid" },
    { updatedAt: "invalid" },
    { createdAt: "2026-02-01T00:00:00Z", updatedAt: date },
  ])("does not restore an invalid pending record %#", async (patch) => {
    const pending = await import("./pending-submission")
    localStorage.setItem(
      "slate.exam-session.quality-user.quality-exam.1.pending",
      JSON.stringify({
        ...scope,
        requestId: "request",
        answers: {},
        status: "buffered",
        createdAt: date,
        updatedAt: date,
        ...patch,
      })
    )
    expect(pending.readPendingSubmission(scope)).toBeNull()
  })

  it("recovers the newest attempt and excludes misplaced records", async () => {
    const pending = await import("./pending-submission")
    const first = pending.createOrLoadPendingSubmission(
      scope,
      {},
      () => "first"
    )
    const second = pending.createOrLoadPendingSubmission(
      { ...scope, attemptNumber: 2 },
      {},
      () => "second"
    )
    localStorage.setItem(
      "slate.exam-session.quality-user.quality-exam.1.pending",
      JSON.stringify({
        ...first,
        createdAt: "2099-01-01T00:00:00Z",
        updatedAt: "2099-01-01T00:00:00Z",
      })
    )
    expect(
      pending.findPendingSubmissionForExam(scope.userId, scope.examId)
        ?.requestId
    ).toBe(second.requestId)
    localStorage.removeItem(
      "slate.exam-session.quality-user.quality-exam.2.pending"
    )
    localStorage.setItem(
      "slate.exam-session.quality-user.quality-exam.99.pending",
      JSON.stringify(second)
    )
    expect(
      pending.findPendingSubmissionForExam(scope.userId, scope.examId)
        ?.requestId
    ).toBe(first.requestId)
  })

  it("restores only canonical draft indices and refuses invalid writes", async () => {
    const pending = await import("./pending-submission")
    localStorage.setItem(
      "slate.exam-session.quality-user.quality-exam.1.draft",
      JSON.stringify({ "0": 1, "00": 0, "-1": 1, "2": 0.5 })
    )
    expect(pending.readExamDraft(scope)).toEqual({ 0: 1 })
    expect(pending.writeExamDraft(scope, { 0: -1 })).toBe(false)
    expect(() =>
      pending.createOrLoadPendingSubmission(scope, {}, () => " ")
    ).toThrow(/invalid data/)
  })

  it.each([
    { attempts: 0 },
    { attempts: 1.5 },
    { attempts: Infinity },
    { delayMs: -1 },
    { maxDelayMs: NaN },
    { delayMs: 2 ** 32 },
  ])("rejects invalid retry settings %#", async (options) => {
    const pending = await import("./pending-submission")
    const record = pending.createOrLoadPendingSubmission(
      scope,
      {},
      () => "request"
    )
    const send = vi.fn()
    await expect(
      pending.submitWithRetry(record, send, options)
    ).rejects.toBeInstanceOf(RangeError)
    expect(send).not.toHaveBeenCalled()
  })

  it("retries 408/500 without allowing the callback to change the snapshot", async () => {
    const pending = await import("./pending-submission")
    const { ApiError } = await import("./api")
    const record = pending.createOrLoadPendingSubmission(
      scope,
      { "0": 1 },
      () => "request"
    )
    const snapshots: number[] = []
    const send = vi.fn(async (payload) => {
      snapshots.push(payload.answers["0"])
      payload.answers["0"] = 0
      if (snapshots.length < 3)
        throw new ApiError("temporary", snapshots.length === 1 ? 408 : 500)
      return "receipt"
    })
    expect(
      await pending.submitWithRetry(record, send, { wait: async () => {} })
    ).toBe("receipt")
    expect(snapshots).toEqual([1, 1, 1])
  })
})

describe("local authoring records", () => {
  it("does not collide when saving two drafts in one millisecond", async () => {
    vi.spyOn(Date, "now").mockReturnValue(42)
    const auth = await import("./auth")
    auth.setAuth(session)
    const drafts = await import("./quiz-drafts")
    const one = drafts.saveQuizDraft({ title: "One", questions: [question] })
    const two = drafts.saveQuizDraft({ title: "Two", questions: [question] })
    expect(one.draft_id).not.toBe(two.draft_id)
    expect(
      renderHook(() => drafts.useQuizDrafts()).result.current
    ).toHaveLength(2)
  })

  it("generates distinct IDs without a secure-context randomUUID", () => {
    vi.stubGlobal("crypto", {})
    expect(localId("draft")).not.toBe(localId("draft"))
  })

  it("drops corrupt nested drafts and deduplicates valid IDs", async () => {
    const auth = await import("./auth")
    auth.setAuth(session)
    const valid = {
      draft_id: "valid",
      title: "Valid",
      updated_at: date,
      questions: [question],
    }
    localStorage.setItem(
      "slate.quiz-drafts.quality-user",
      JSON.stringify([
        valid,
        valid,
        { ...valid, draft_id: "broken", questions: [null] },
        { ...valid, draft_id: "settings", instructions: {} },
        { ...valid, draft_id: "date", updated_at: "bad" },
      ])
    )
    const drafts = await import("./quiz-drafts")
    expect(renderHook(() => drafts.useQuizDrafts()).result.current).toEqual([
      valid,
    ])
  })

  it.each([
    { id: " " },
    { saved_at: "bad" },
    { options: ["one"] },
    { options: ["", "two"] },
    { answer: 4 },
    { points: -1 },
    { feedback: {} },
  ])("excludes invalid bank records %#", async (patch) => {
    const auth = await import("./auth")
    auth.setAuth(session)
    localStorage.setItem(
      "slate.question-bank.quality-user",
      JSON.stringify([
        { ...question, id: "question", saved_at: date, ...patch },
      ])
    )
    const bank = await import("./question-bank")
    expect(renderHook(() => bank.useQuestionBank()).result.current).toEqual([])
  })
})

describe("board history consistency", () => {
  it("validates, deduplicates and orders entries before applying the limit", async () => {
    const valid = {
      room_id: "room",
      first_opened_at: date,
      last_opened_at: date,
      visits: 1,
      source: "local-history",
    }
    localStorage.setItem(
      "slate.board-history",
      JSON.stringify([
        { ...valid, room_id: "old", last_opened_at: "2026-01-02T00:00:00Z" },
        valid,
        valid,
        { ...valid, room_id: "" },
        { ...valid, room_id: "bad", visits: 0 },
        { ...valid, room_id: "invalid", first_opened_at: "bad" },
      ])
    )
    const history = await import("./board-history")
    const hook = renderHook(() => history.useBoardHistory())
    expect(hook.result.current.map((entry) => entry.room_id)).toEqual([
      "old",
      "room",
    ])
    act(() => {
      localStorage.setItem("slate.board-history", "[]")
      window.dispatchEvent(
        new StorageEvent("storage", { key: "slate.board-history" })
      )
    })
    expect(hook.result.current).toEqual([])
    act(() => history.recordBoardVisit(" "))
    expect(hook.result.current).toEqual([])
  })

  it("orders mixed timezones by the actual instant", async () => {
    const history = await import("./board-history")
    const entry = {
      first_opened_at: date,
      visits: 1,
      source: "local-history" as const,
    }
    expect(
      history.mergeBoardHistory(
        [],
        [
          {
            ...entry,
            room_id: "older",
            last_opened_at: "2026-01-02T01:00:00+05:00",
          },
          {
            ...entry,
            room_id: "newer",
            last_opened_at: "2026-01-01T22:00:00Z",
          },
        ]
      )[0].room_id
    ).toBe("newer")
  })

  it("deduplicates recent rooms and responds to storage updates", async () => {
    localStorage.setItem(
      "slate.recent-rooms",
      JSON.stringify(["", "room", "room", 4])
    )
    const recent = await import("./recent-rooms")
    const hook = renderHook(() => recent.useRecentRooms())
    expect(hook.result.current).toEqual(["room"])
    act(() => {
      localStorage.setItem("slate.recent-rooms", '["next"]')
      window.dispatchEvent(
        new StorageEvent("storage", { key: "slate.recent-rooms" })
      )
    })
    expect(hook.result.current).toEqual(["next"])
  })
})

describe("navigation and import boundaries", () => {
  it("bounds corrupt progress and flag targets", () => {
    expect(readQuizProgress(scope, NaN)).toEqual({
      position: 0,
      flaggedIndices: [],
    })
    expect(writeQuizProgress(scope, { position: -1, flaggedIndices: [] })).toBe(
      false
    )
    expect(toggleFlaggedQuestion([1], -1)).toEqual([1])
    expect(visibleQuestionPositions(Infinity, "all", [], {})).toEqual([])
  })

  it("sorts invalid deadlines last and rejects corrupt retake counters", () => {
    const exam = {
      id: "one",
      title: "A",
      created_at: date,
      question_count: 1,
    } satisfies ExamMeta
    const sorted = filterAndSortAssessments(
      [
        { ...exam, closes_at: "invalid" },
        { ...exam, id: "two", title: "Z", closes_at: date },
      ],
      { search: "", status: "all", classroomId: "all", sort: "deadline" }
    )
    expect(sorted[0].id).toBe("two")
    expect(
      canRetakeAssessment({
        ...exam,
        attempt_status: "submitted",
        max_attempts: -1,
      })
    ).toBe(false)
    expect(
      assessmentAttemptStatus({ ...exam, attempt_status: "unknown" as never })
    ).toBe("not_started")
  })

  it("accepts BOM-prefixed JSON and rejects oversized text", () => {
    expect(
      parseQuestionImport(
        JSON.stringify([{ ...question, points: 1.5 }]),
        "json"
      ).errors[0]
    ).toContain("whole number")
    expect(
      parseQuestionImport("\uFEFF" + JSON.stringify([question]), "json")
        .questions
    ).toEqual([question])
    expect(
      parseQuestionImport("x".repeat(1_000_001), "json").errors[0]
    ).toContain("1,000,000")
  })

  it.each([
    "question,prompt,option_a,option_b,answer\nQ,Other,A,B,0",
    "question,option_a,option_b,answer\nQ,A,B,0,extra",
    "question,option_a,option_b,answer,points\nQ,A,B,0,0x10",
  ])("rejects ambiguous or lossy CSV %#", (source) => {
    expect(parseQuestionImport(source, "csv").errors).not.toHaveLength(0)
  })
})
