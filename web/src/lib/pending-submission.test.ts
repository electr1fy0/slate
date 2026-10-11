import { afterEach, describe, expect, it, vi } from "vitest"

import { ApiError } from "./api"
import {
  createOrLoadPendingSubmission,
  findPendingSubmissionForExam,
  readExamDraft,
  readPendingSubmission,
  submitWithRetry,
  writeExamDraft,
} from "./pending-submission"

const scope = { userId: "student-1", examId: "exam-1", attemptNumber: 2 }

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

describe("pending exam submission", () => {
  it("scopes answer drafts and pending requests by student, exam, and attempt", () => {
    writeExamDraft(scope, { 0: 1 })
    const pending = createOrLoadPendingSubmission(
      scope,
      { "0": 1 },
      () => "request-1"
    )

    expect(readExamDraft(scope)).toEqual({ 0: 1 })
    expect(readExamDraft({ ...scope, userId: "student-2" })).toEqual({})
    expect(readExamDraft({ ...scope, attemptNumber: 3 })).toEqual({})
    expect(readPendingSubmission({ ...scope, attemptNumber: 3 })).toBeNull()
    expect(
      findPendingSubmissionForExam(scope.userId, scope.examId)
    ).toMatchObject({
      requestId: "request-1",
      attemptNumber: 2,
    })
    expect(findPendingSubmissionForExam("student-2", scope.examId)).toBeNull()
    expect(pending.answers).toEqual({ "0": 1 })
  })

  it("keeps the original request ID and payload when a retry sees newer edited answers", () => {
    const first = createOrLoadPendingSubmission(
      scope,
      { "0": 1, "1": 0 },
      () => "fixed-request"
    )
    const retry = createOrLoadPendingSubmission(
      scope,
      { "0": 0, "1": 1 },
      () => "must-not-be-used"
    )

    expect(retry.requestId).toBe(first.requestId)
    expect(retry.answers).toEqual({ "0": 1, "1": 0 })
  })

  it("retries transient failures with the identical payload and request ID", async () => {
    const pending = createOrLoadPendingSubmission(
      scope,
      { "0": 1 },
      () => "stable-request"
    )
    const send = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("network disconnected"))
      .mockRejectedValueOnce(
        new ApiError("writer was fenced", 409, "WRITER_FENCED")
      )
      .mockRejectedValueOnce(new ApiError("throttled", 429))
      .mockResolvedValueOnce({
        request_id: "stable-request",
        status: "duplicate",
      })
    const wait = vi.fn(() => Promise.resolve())

    const receipt = await submitWithRetry(pending, send, { wait })

    expect(receipt).toEqual({
      request_id: "stable-request",
      status: "duplicate",
    })
    expect(send).toHaveBeenCalledTimes(4)
    expect(send.mock.calls.map(([payload]) => payload)).toEqual([
      { request_id: "stable-request", answers: { "0": 1 } },
      { request_id: "stable-request", answers: { "0": 1 } },
      { request_id: "stable-request", answers: { "0": 1 } },
      { request_id: "stable-request", answers: { "0": 1 } },
    ])
    expect(wait).toHaveBeenCalledTimes(3)
  })

  it("uses a finite default recovery window with capped exponential backoff", async () => {
    const pending = createOrLoadPendingSubmission(
      scope,
      { "0": 1, "1": 0 },
      () => "long-recovery-request"
    )
    const send = vi.fn()
    for (let attempt = 0; attempt < 15; attempt += 1) {
      send.mockRejectedValueOnce(new ApiError("temporarily unavailable", 503))
    }
    send.mockResolvedValueOnce({ request_id: "long-recovery-request" })
    const delays: number[] = []

    const receipt = await submitWithRetry(pending, send, {
      wait: async (ms) => {
        delays.push(ms)
      },
    })

    expect(receipt).toEqual({ request_id: "long-recovery-request" })
    expect(send).toHaveBeenCalledTimes(16)
    expect(delays).toEqual([
      1000,
      2000,
      4000,
      8000,
      ...Array<number>(11).fill(10_000),
    ])
    expect(delays.reduce((total, delay) => total + delay, 0)).toBe(125_000)
    expect(Math.max(...delays)).toBe(10_000)
    expect(send.mock.calls.map(([payload]) => payload)).toEqual(
      Array.from({ length: 16 }, () => ({
        request_id: "long-recovery-request",
        answers: { "0": 1, "1": 0 },
      }))
    )
  })

  it("stops immediately for terminal errors and leaves the persisted request intact", async () => {
    const pending = createOrLoadPendingSubmission(
      scope,
      { "0": 1 },
      () => "stable-request"
    )
    const send = vi
      .fn()
      .mockRejectedValue(new ApiError("exam closed", 403, "EXAM_CLOSED"))

    await expect(
      submitWithRetry(pending, send, { wait: vi.fn() })
    ).rejects.toThrow("exam closed")

    expect(send).toHaveBeenCalledTimes(1)
    expect(readPendingSubmission(scope)).toMatchObject({
      requestId: "stable-request",
      answers: { "0": 1 },
    })

    const conflict = vi.fn().mockRejectedValue(new ApiError("already submitted", 409))
    await expect(submitWithRetry(pending, conflict, { wait: vi.fn() })).rejects.toThrow("already submitted")
    expect(conflict).toHaveBeenCalledTimes(1)
  })
})
