import { beforeEach, describe, expect, it } from "vitest"

import {
  readQuizProgress,
  toggleFlaggedQuestion,
  visibleQuestionPositions,
  writeQuizProgress,
} from "./quiz-progress"

const scope = { userId: "learner-a", examId: "exam-a", attemptNumber: 2 }

describe("quiz progress", () => {
  beforeEach(() => localStorage.clear())

  it("persists flags and position within an account and attempt scope", () => {
    expect(
      writeQuizProgress(scope, { position: 2, flaggedIndices: [0, 2] })
    ).toBe(true)
    expect(readQuizProgress(scope, 4)).toEqual({
      position: 2,
      flaggedIndices: [0, 2],
    })
    expect(readQuizProgress({ ...scope, userId: "learner-b" }, 4)).toEqual({
      position: 0,
      flaggedIndices: [],
    })
    expect(readQuizProgress({ ...scope, attemptNumber: 3 }, 4)).toEqual({
      position: 0,
      flaggedIndices: [],
    })
  })

  it("clamps stale positions, drops invalid flags, and filters unanswered questions", () => {
    localStorage.setItem(
      "slate.quiz-progress.learner-a.exam-a.2",
      JSON.stringify({
        position: 8,
        flaggedIndices: [0, 1, 9, "2"],
      })
    )
    expect(readQuizProgress(scope, 3)).toEqual({
      position: 2,
      flaggedIndices: [0, 1],
    })
    expect(toggleFlaggedQuestion([0], 1)).toEqual([0, 1])
    expect(toggleFlaggedQuestion([0, 1], 0)).toEqual([1])
    expect(visibleQuestionPositions(4, "flagged", [1, 3], {})).toEqual([1, 3])
    expect(
      visibleQuestionPositions(4, "unanswered", [], { 0: 1, 3: 0 })
    ).toEqual([1, 2])
  })
})
