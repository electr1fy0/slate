import { describe, expect, it } from "vitest"

import { formatResultReceipt } from "./result-receipt"
import type { ResultResponse } from "./types"

describe("result receipt", () => {
  it("includes receipt identity, points, explanations, and skipped answers", () => {
    const result: ResultResponse = {
      status: "committed",
      score: 2,
      total: 3,
      answers: { "0": 1 },
      submitted_at: "2026-10-11T10:00:00Z",
      request_id: "receipt-1",
      review: [
        {
          index: 0,
          question: "Which?",
          options: ["A", "B"],
          chosen: 1,
          correct_answer: 1,
          was_correct: true,
          points: 2,
          feedback: "Good work.",
        },
        {
          index: 1,
          question: "Skipped?",
          options: ["C", "D"],
          chosen: null,
          correct_answer: 0,
          was_correct: false,
        },
      ],
    }
    const receipt = formatResultReceipt(result)
    expect(receipt).toContain("Score: 2/3 (67%)")
    expect(receipt).toContain("Receipt reference: receipt-1")
    expect(receipt).toContain("Points: 2/2")
    expect(receipt).toContain("Feedback: Good work.")
    expect(receipt).toContain("Your answer: No answer")
  })
})
