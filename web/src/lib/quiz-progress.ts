import type { ExamScope } from "./pending-submission"

export interface QuizProgress {
  position: number
  flaggedIndices: number[]
}

function key(scope: ExamScope): string {
  return `slate.quiz-progress.${encodeURIComponent(scope.userId)}.${encodeURIComponent(scope.examId)}.${scope.attemptNumber}`
}

export function readQuizProgress(
  scope: ExamScope,
  questionCount: number
): QuizProgress {
  try {
    if (typeof window === "undefined")
      return { position: 0, flaggedIndices: [] }
    const raw = window.localStorage.getItem(key(scope))
    if (!raw) return { position: 0, flaggedIndices: [] }
    const value: unknown = JSON.parse(raw)
    if (!value || typeof value !== "object")
      return { position: 0, flaggedIndices: [] }
    const record = value as Record<string, unknown>
    const position =
      typeof record.position === "number" && Number.isInteger(record.position)
        ? Math.min(Math.max(record.position, 0), Math.max(questionCount - 1, 0))
        : 0
    const flaggedIndices = Array.isArray(record.flaggedIndices)
      ? Array.from(
          new Set(
            record.flaggedIndices.filter(
              (index): index is number =>
                typeof index === "number" &&
                Number.isInteger(index) &&
                index >= 0 &&
                index < questionCount
            )
          )
        )
      : []
    return { position, flaggedIndices }
  } catch {
    return { position: 0, flaggedIndices: [] }
  }
}

export function writeQuizProgress(
  scope: ExamScope,
  progress: QuizProgress
): boolean {
  try {
    if (typeof window === "undefined") return false
    window.localStorage.setItem(key(scope), JSON.stringify(progress))
    return true
  } catch {
    return false
  }
}

export function toggleFlaggedQuestion(
  indices: number[],
  index: number
): number[] {
  return indices.includes(index)
    ? indices.filter((item) => item !== index)
    : [...indices, index].sort((a, b) => a - b)
}

export function visibleQuestionPositions(
  questionCount: number,
  filter: "all" | "flagged" | "unanswered",
  flaggedIndices: number[],
  answers: Partial<Record<number, number>>
): number[] {
  const positions = Array.from({ length: questionCount }, (_, index) => index)
  if (filter === "flagged")
    return positions.filter((index) => flaggedIndices.includes(index))
  if (filter === "unanswered")
    return positions.filter((index) => answers[index] === undefined)
  return positions
}
