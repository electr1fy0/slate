import type { ResultResponse } from "./types"

export function formatResultReceipt(result: ResultResponse): string {
  const percent =
    result.total > 0 ? Math.round((result.score / result.total) * 100) : 0
  const lines = [
    "Continuum assessment receipt",
    `Score: ${result.score}/${result.total} (${percent}%)`,
    result.submitted_at ? `Submitted: ${result.submitted_at}` : undefined,
    result.request_id ? `Receipt reference: ${result.request_id}` : undefined,
    "",
    ...result.review.flatMap((question) => {
      const chosen =
        question.chosen === null
          ? "No answer"
          : (question.options[question.chosen] ?? "Unknown answer")
      const correct =
        question.correct_answer === null
          ? "Unknown"
          : (question.options[question.correct_answer] ?? "Unknown")
      return [
        `Question ${question.index + 1}: ${question.question}`,
        `Your answer: ${chosen}`,
        `Correct answer: ${correct}`,
        ...(question.points === undefined
          ? []
          : [
              `Points: ${question.was_correct ? question.points : 0}/${question.points}`,
            ]),
        ...(question.feedback ? [`Feedback: ${question.feedback}`] : []),
        "",
      ]
    }),
  ].filter((line): line is string => line !== undefined)
  return lines.join("\n").trim()
}
