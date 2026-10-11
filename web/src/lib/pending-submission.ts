import { ApiError } from "./api"

export interface ExamScope {
  userId: string
  examId: string
  attemptNumber: number
}

export type PendingSubmissionStatus = "buffered" | "submitting" | "blocked"

export interface PendingSubmission {
  userId: string
  examId: string
  attemptNumber: number
  requestId: string
  answers: Record<string, number>
  status: PendingSubmissionStatus
  createdAt: string
  updatedAt: string
}

export interface PendingSubmissionPayload {
  request_id: string
  answers: Record<string, number>
  attempt_number?: number
}

const STORAGE_PREFIX = "slate.exam-session"

function encoded(value: string): string {
  return encodeURIComponent(value)
}

function sessionPrefix(scope: Pick<ExamScope, "userId" | "examId">): string {
  return `${STORAGE_PREFIX}.${encoded(scope.userId)}.${encoded(scope.examId)}`
}

function draftKey(scope: ExamScope): string {
  return `${sessionPrefix(scope)}.${scope.attemptNumber}.draft`
}

function pendingKey(scope: ExamScope): string {
  return `${sessionPrefix(scope)}.${scope.attemptNumber}.pending`
}

function storage(): Storage | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage
  } catch {
    return null
  }
}

function validAnswers(value: unknown): value is Record<string, number> {
  return (
    !!value &&
    typeof value === "object" &&
    Object.values(value).every(
      (answer) => typeof answer === "number" && Number.isFinite(answer)
    )
  )
}

function isPendingSubmission(value: unknown): value is PendingSubmission {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<PendingSubmission>
  return (
    typeof candidate.userId === "string" &&
    typeof candidate.examId === "string" &&
    typeof candidate.attemptNumber === "number" &&
    typeof candidate.requestId === "string" &&
    candidate.requestId.length > 0 &&
    validAnswers(candidate.answers) &&
    (candidate.status === "buffered" ||
      candidate.status === "submitting" ||
      candidate.status === "blocked") &&
    typeof candidate.createdAt === "string" &&
    typeof candidate.updatedAt === "string"
  )
}

function readPending(key: string): PendingSubmission | null {
  try {
    const raw = storage()?.getItem(key)
    if (!raw) return null
    const parsed: unknown = JSON.parse(raw)
    return isPendingSubmission(parsed) ? parsed : null
  } catch {
    return null
  }
}

export function readExamDraft(
  scope: ExamScope
): Partial<Record<number, number>> {
  try {
    const raw = storage()?.getItem(draftKey(scope))
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
      return {}
    const answers: Partial<Record<number, number>> = {}
    for (const [key, value] of Object.entries(
      parsed as Record<string, unknown>
    )) {
      const index = Number(key)
      if (
        Number.isInteger(index) &&
        typeof value === "number" &&
        Number.isFinite(value)
      ) {
        answers[index] = value
      }
    }
    return answers
  } catch {
    return {}
  }
}

export function writeExamDraft(
  scope: ExamScope,
  answers: Partial<Record<number, number>>
): boolean {
  const target = storage()
  if (!target) return false
  try {
    target.setItem(draftKey(scope), JSON.stringify(answers))
    return true
  } catch {
    return false
  }
}

export function readPendingSubmission(
  scope: ExamScope
): PendingSubmission | null {
  const pending = readPending(pendingKey(scope))
  return pending &&
    pending.userId === scope.userId &&
    pending.examId === scope.examId &&
    pending.attemptNumber === scope.attemptNumber
    ? pending
    : null
}

export function findPendingSubmissionForExam(
  userId: string,
  examId: string
): PendingSubmission | null {
  const target = storage()
  if (!target) return null
  const prefix = `${sessionPrefix({ userId, examId })}.`
  let newest: PendingSubmission | null = null
  try {
    for (let index = 0; index < target.length; index += 1) {
      const key = target.key(index)
      if (!key?.startsWith(prefix) || !key.endsWith(".pending")) continue
      const pending = readPending(key)
      if (!pending || pending.userId !== userId || pending.examId !== examId)
        continue
      if (!newest || pending.createdAt > newest.createdAt) newest = pending
    }
  } catch {
    return newest
  }
  return newest
}

export function createOrLoadPendingSubmission(
  scope: ExamScope,
  answers: Record<string, number>,
  requestIdFactory: () => string = createRequestId
): PendingSubmission {
  const existing = readPendingSubmission(scope)
  if (existing) return existing

  const now = new Date().toISOString()
  const pending: PendingSubmission = {
    ...scope,
    requestId: requestIdFactory(),
    answers: { ...answers },
    status: "buffered",
    createdAt: now,
    updatedAt: now,
  }
  const target = storage()
  if (!target)
    throw new Error("This browser cannot save a pending submission safely.")
  try {
    target.setItem(pendingKey(scope), JSON.stringify(pending))
  } catch {
    throw new Error(
      "Your pending submission could not be saved on this device. Try again after storage is available."
    )
  }
  return pending
}

function createRequestId(): string {
  if (
    typeof crypto !== "undefined" &&
    typeof crypto.randomUUID === "function"
  ) {
    return crypto.randomUUID()
  }
  return `submission-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

export function setPendingSubmissionStatus(
  scope: ExamScope,
  requestId: string,
  status: PendingSubmissionStatus
): boolean {
  const pending = readPendingSubmission(scope)
  const target = storage()
  if (!pending || pending.requestId !== requestId || !target) return false
  try {
    target.setItem(
      pendingKey(scope),
      JSON.stringify({
        ...pending,
        status,
        updatedAt: new Date().toISOString(),
      } satisfies PendingSubmission)
    )
    return true
  } catch {
    return false
  }
}

export function clearPendingSubmission(
  scope: ExamScope,
  requestId: string
): boolean {
  const pending = readPendingSubmission(scope)
  const target = storage()
  if (!pending || pending.requestId !== requestId || !target) return false
  try {
    target.removeItem(pendingKey(scope))
    return true
  } catch {
    return false
  }
}

export function clearExamDraft(scope: ExamScope): void {
  try {
    storage()?.removeItem(draftKey(scope))
  } catch {
    // Keep the durable submission path independent of draft cleanup.
  }
}

export function pendingPayload(
  pending: PendingSubmission
): PendingSubmissionPayload {
  return {
    request_id: pending.requestId,
    answers: { ...pending.answers },
    // Preserve the original request hash for receipts created before retakes.
    ...(pending.attemptNumber > 1 ? { attempt_number: pending.attemptNumber } : {}),
  }
}

export function isTransientSubmissionError(error: unknown): boolean {
  if (error instanceof ApiError) {
    return (
      error.code === "WRITER_FENCED" || [429, 502, 503, 504].includes(error.status)
    )
  }
  return error instanceof TypeError
}

export async function submitWithRetry<T>(
  pending: PendingSubmission,
  submit: (payload: PendingSubmissionPayload) => Promise<T>,
  options: {
    attempts?: number
    delayMs?: number
    maxDelayMs?: number
    wait?: (ms: number) => Promise<void>
  } = {}
): Promise<T> {
  const attempts = options.attempts ?? 16
  const delayMs = options.delayMs ?? 1000
  const maxDelayMs = options.maxDelayMs ?? 10_000
  const wait =
    options.wait ??
    ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)))
  const payload = pendingPayload(pending)

  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      return await submit(payload)
    } catch (error) {
      if (!isTransientSubmissionError(error) || attempt === attempts - 1)
        throw error
      await wait(Math.min(delayMs * 2 ** attempt, maxDelayMs))
    }
  }
  throw new Error("Submission retry loop ended unexpectedly.")
}
