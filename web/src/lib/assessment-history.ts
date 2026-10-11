import { examState } from "./types"
import type { ExamMeta, ResultResponse } from "./types"

export type AssessmentFilterStatus =
  "all" | "not_started" | "in_progress" | "submitted"
export type AssessmentSort = "recent" | "title" | "deadline"

export interface AssessmentHistoryFilters {
  search: string
  status: AssessmentFilterStatus
  classroomId: string
  sort: AssessmentSort
}

export function assessmentAttemptStatus(
  exam: ExamMeta
): Exclude<AssessmentFilterStatus, "all"> {
  return exam.attempt_status ?? "not_started"
}

export function filterAndSortAssessments(
  exams: ExamMeta[],
  filters: AssessmentHistoryFilters
): ExamMeta[] {
  const search = filters.search.trim().toLocaleLowerCase()
  const filtered = exams.filter((exam) => {
    if (search && !exam.title.toLocaleLowerCase().includes(search)) return false
    if (
      filters.status !== "all" &&
      assessmentAttemptStatus(exam) !== filters.status
    )
      return false
    if (
      filters.classroomId !== "all" &&
      (exam.classroom_id ?? "unassigned") !== filters.classroomId
    )
      return false
    return true
  })

  return filtered.sort((left, right) => {
    if (filters.sort === "title") return left.title.localeCompare(right.title)
    if (filters.sort === "deadline") {
      const leftTime = left.closes_at
        ? Date.parse(left.closes_at)
        : Number.POSITIVE_INFINITY
      const rightTime = right.closes_at
        ? Date.parse(right.closes_at)
        : Number.POSITIVE_INFINITY
      return leftTime - rightTime || left.title.localeCompare(right.title)
    }
    return (
      Date.parse(right.created_at) - Date.parse(left.created_at) ||
      left.title.localeCompare(right.title)
    )
  })
}

export function assessmentActionLabel(
  exam: ExamMeta
): "Start assessment" | "Resume assessment" | "View result" {
  const status = assessmentAttemptStatus(exam)
  if (status === "submitted") return "View result"
  if (status === "in_progress") return "Resume assessment"
  return "Start assessment"
}

export function assessmentStatusLabel(
  exam: ExamMeta,
  now = Date.now()
): string {
  const attemptStatus = assessmentAttemptStatus(exam)
  if (attemptStatus === "submitted") return "submitted"
  if (attemptStatus === "in_progress") return "in progress"
  return examState(exam, now)
}

export function canRetakeAssessment(exam: ExamMeta, now = Date.now()): boolean {
  if (assessmentAttemptStatus(exam) !== "submitted") return false
  const attemptNumber = exam.attempt_number ?? 1
  const maxAttempts = exam.max_attempts ?? 1
  if (maxAttempts !== 0 && attemptNumber >= maxAttempts) return false

  return isAssessmentWindowOpen(exam, now)
}

export function isAssessmentWindowOpen(
  exam: ExamMeta,
  now = Date.now()
): boolean {
  const state = examState(exam, now)
  if (state === "open") return true
  if (state !== "closed" || !exam.allow_late || !exam.closes_at) return false
  const grace = exam.late_grace_minutes ?? 0
  if (grace <= 0) return true
  const lateDeadline = Date.parse(exam.closes_at) + grace * 60_000
  return now <= lateDeadline
}

// A confirmed result is authoritative even if cached metadata still says the
// same attempt is in progress. An older receipt must not hide a fresh retake.
export function isResultForCurrentAttempt(
  result: Pick<ResultResponse, "attempt_number">,
  meta?: Pick<ExamMeta, "attempt_number">
): boolean {
  return (result.attempt_number ?? 1) >= (meta?.attempt_number ?? 1)
}
