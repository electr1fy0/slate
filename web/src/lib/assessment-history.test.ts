import { describe, expect, it } from "vitest"

import {
  assessmentActionLabel,
  assessmentStatusLabel,
  canRetakeAssessment,
  filterAndSortAssessments,
  isResultForCurrentAttempt,
} from "./assessment-history"
import type { ExamMeta } from "./types"

const exams: ExamMeta[] = [
  {
    id: "submitted-closed",
    title: "Closed systems",
    question_count: 2,
    created_at: "2026-01-02T00:00:00Z",
    closes_at: "2026-01-03T00:00:00Z",
    classroom_id: "class-a",
    attempt_status: "submitted",
    score: 1,
    total: 2,
  },
  {
    id: "working",
    title: "Open networks",
    question_count: 4,
    created_at: "2026-01-03T00:00:00Z",
    classroom_id: "class-b",
    attempt_status: "in_progress",
  },
  {
    id: "new",
    title: "Database basics",
    question_count: 3,
    created_at: "2026-01-04T00:00:00Z",
    classroom_id: "class-a",
    opens_at: "2026-02-01T00:00:00Z",
  },
]

describe("assessment history", () => {
  it("shows a current receipt despite stale metadata and hides it during a later retake", () => {
    expect(isResultForCurrentAttempt({}, undefined)).toBe(true)
    expect(isResultForCurrentAttempt({}, { attempt_number: 1 })).toBe(true)
    expect(
      isResultForCurrentAttempt({ attempt_number: 1 }, { attempt_number: 2 })
    ).toBe(false)
    expect(
      isResultForCurrentAttempt({ attempt_number: 2 }, { attempt_number: 2 })
    ).toBe(true)
    expect(
      isResultForCurrentAttempt({ attempt_number: 2 }, { attempt_number: 1 })
    ).toBe(true)
  })
  it("keeps submitted results available when an exam is closed", () => {
    expect(assessmentActionLabel(exams[0])).toBe("View result")
    expect(
      assessmentStatusLabel(exams[0], Date.parse("2026-02-01T00:00:00Z"))
    ).toBe("submitted")
  })

  it("only enables retakes when attempt and availability metadata allow them", () => {
    expect(
      canRetakeAssessment({
        ...exams[0],
        closes_at: "2026-12-31T00:00:00.000Z",
        attempt_status: "submitted",
        attempt_number: 1,
        max_attempts: 2,
      })
    ).toBe(true)
    expect(
      canRetakeAssessment({
        ...exams[0],
        attempt_status: "submitted",
        attempt_number: 2,
        max_attempts: 2,
      })
    ).toBe(false)
    expect(
      canRetakeAssessment({
        ...exams[0],
        closes_at: "2026-12-31T00:00:00.000Z",
        attempt_status: "submitted",
        attempt_number: 4,
        max_attempts: 0,
      })
    ).toBe(true)
    expect(
      canRetakeAssessment({
        ...exams[0],
        attempt_status: "submitted",
        attempt_number: 1,
      })
    ).toBe(false)
    expect(
      canRetakeAssessment(
        {
          ...exams[0],
          closes_at: "2026-01-01T00:00:00.000Z",
          attempt_status: "submitted",
          attempt_number: 1,
          max_attempts: 2,
          allow_late: true,
          late_grace_minutes: 10,
        },
        Date.parse("2026-01-01T00:05:00.000Z")
      )
    ).toBe(true)
  })

  it("searches, filters by attempt state and classroom, and sorts results", () => {
    const selected = filterAndSortAssessments(exams, {
      search: "systems",
      status: "submitted",
      classroomId: "class-a",
      sort: "title",
    })
    expect(selected.map((exam) => exam.id)).toEqual(["submitted-closed"])
    expect(
      filterAndSortAssessments(exams, {
        search: "",
        status: "in_progress",
        classroomId: "all",
        sort: "recent",
      }).map((exam) => exam.id)
    ).toEqual(["working"])
    expect(
      filterAndSortAssessments(exams, {
        search: "",
        status: "all",
        classroomId: "class-a",
        sort: "deadline",
      }).map((exam) => exam.id)
    ).toEqual(["submitted-closed", "new"])
  })
})
