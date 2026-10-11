import type { QuestionDraft } from "./types"

export interface ImportResult {
  questions: QuestionDraft[]
  errors: string[]
}

export function toLocalDateTime(value?: string): string {
  if (!value) return ""
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return ""
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

export function fromLocalDateTime(value: string): string | undefined {
  if (!value.trim()) return undefined
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toISOString() : undefined
}

function normaliseQuestion(
  value: unknown,
  rowLabel: string
): { question?: QuestionDraft; error?: string } {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {
      error: `${rowLabel}: expected an object with question, options, and answer.`,
    }
  }
  const candidate = value as Record<string, unknown>
  const prompt =
    typeof candidate.question === "string" ? candidate.question.trim() : ""
  const options = Array.isArray(candidate.options)
    ? candidate.options.map((option) =>
        typeof option === "string" ? option.trim() : ""
      )
    : []
  const answer = candidate.answer
  const points = candidate.points
  const feedback = candidate.feedback

  if (!prompt) return { error: `${rowLabel}: question text is required.` }
  if (
    options.length < 2 ||
    options.length > 6 ||
    options.some((option) => !option)
  ) {
    return { error: `${rowLabel}: provide 2–6 non-empty options.` }
  }
  if (
    !Number.isInteger(answer) ||
    Number(answer) < 0 ||
    Number(answer) >= options.length
  ) {
    return {
      error: `${rowLabel}: answer must be a zero-based option index from 0 to ${options.length - 1}.`,
    }
  }
  if (
    points !== undefined &&
    (typeof points !== "number" ||
      !Number.isFinite(points) ||
      points < 1 ||
      points > 1000)
  ) {
    return { error: `${rowLabel}: points must be a number from 1 to 1000.` }
  }
  if (feedback !== undefined && typeof feedback !== "string") {
    return { error: `${rowLabel}: feedback must be text.` }
  }
  return {
    question: {
      question: prompt,
      options,
      answer: Number(answer),
      ...(points === undefined ? {} : { points }),
      ...(typeof feedback === "string" && feedback.trim()
        ? { feedback: feedback.trim() }
        : {}),
    },
  }
}

function parseJsonQuestions(source: string): ImportResult {
  let parsed: unknown
  try {
    parsed = JSON.parse(source)
  } catch (error) {
    return {
      questions: [],
      errors: [
        `Invalid JSON: ${error instanceof Error ? error.message : "could not parse input"}`,
      ],
    }
  }
  const rows = Array.isArray(parsed)
    ? parsed
    : parsed &&
        typeof parsed === "object" &&
        Array.isArray((parsed as Record<string, unknown>).questions)
      ? (parsed as { questions: unknown[] }).questions
      : null
  if (!rows)
    return {
      questions: [],
      errors: [
        "JSON must be an array of questions or an object with a questions array.",
      ],
    }
  const questions: QuestionDraft[] = []
  const errors: string[] = []
  rows.forEach((row, index) => {
    const result = normaliseQuestion(row, `Question ${index + 1}`)
    if (result.question) questions.push(result.question)
    if (result.error) errors.push(result.error)
  })
  if (rows.length === 0) errors.push("Add at least one question to import.")
  return { questions, errors }
}

function parseCsvRows(source: string): string[][] {
  const rows: string[][] = []
  let row: string[] = []
  let cell = ""
  let inQuotes = false
  for (let index = 0; index < source.length; index += 1) {
    const character = source[index]
    if (character === '"') {
      if (inQuotes && source[index + 1] === '"') {
        cell += '"'
        index += 1
      } else {
        inQuotes = !inQuotes
      }
    } else if (character === "," && !inQuotes) {
      row.push(cell)
      cell = ""
    } else if ((character === "\n" || character === "\r") && !inQuotes) {
      if (character === "\r" && source[index + 1] === "\n") index += 1
      row.push(cell)
      if (row.some((part) => part.trim())) rows.push(row)
      row = []
      cell = ""
    } else {
      cell += character
    }
  }
  if (inQuotes) throw new Error("A quoted CSV field was not closed.")
  row.push(cell)
  if (row.some((part) => part.trim())) rows.push(row)
  return rows
}

function parseCsvQuestions(source: string): ImportResult {
  let rows: string[][]
  try {
    rows = parseCsvRows(source)
  } catch (error) {
    return {
      questions: [],
      errors: [error instanceof Error ? error.message : "Invalid CSV."],
    }
  }
  if (rows.length < 2)
    return {
      questions: [],
      errors: ["CSV needs a header row and at least one question row."],
    }

  const headers = rows[0].map((header) =>
    header.trim().toLowerCase().replace(/[ -]+/g, "_")
  )
  const column = (...names: string[]): number =>
    headers.findIndex((header) => names.includes(header))
  const questionIndex = column("question", "prompt")
  const answerIndex = column("answer", "correct", "correct_answer")
  const pointsIndex = column("points")
  const feedbackIndex = column("feedback")
  const optionColumns: {
    index: number
    family: "letter" | "number"
    ordinal: number
  }[] = []
  headers.forEach((header, index) => {
    const lettered = /^(?:option|choice)_?([a-f])$/.exec(header)
    if (lettered) {
      const suffix = lettered[1]
      optionColumns.push({
        index,
        family: "letter",
        ordinal: suffix.charCodeAt(0) - 96,
      })
      return
    }
    const numbered = /^option_?(\d+)$/.exec(header)
    if (numbered) {
      optionColumns.push({
        index,
        family: "number",
        ordinal: Number(numbered[1]),
      })
    }
  })
  const optionIndexes = optionColumns
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .map(({ index }) => index)
  if (questionIndex < 0 || answerIndex < 0 || optionIndexes.length < 2) {
    return {
      questions: [],
      errors: [
        "CSV headers must include question, at least two option_a…option_f or option_1…option_6 columns, and answer.",
      ],
    }
  }
  if (new Set(optionColumns.map(({ family }) => family)).size > 1) {
    return {
      questions: [],
      errors: [
        "CSV option headers must use one scheme: lettered (option_a…option_f) or numbered (option_1…option_6).",
      ],
    }
  }
  if (optionColumns.length > 6) {
    return {
      questions: [],
      errors: ["CSV can contain at most six option columns."],
    }
  }
  const sortedOptions = optionColumns
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
  if (
    new Set(sortedOptions.map(({ ordinal }) => ordinal)).size !==
    sortedOptions.length
  ) {
    return {
      questions: [],
      errors: ["CSV option columns must have unique suffixes."],
    }
  }
  if (sortedOptions.some(({ ordinal }, index) => ordinal !== index + 1)) {
    return {
      questions: [],
      errors: [
        "CSV option columns must be contiguous and start at option_a or option_1.",
      ],
    }
  }

  const questions: QuestionDraft[] = []
  const errors: string[] = []
  rows.slice(1).forEach((row, rowIndex) => {
    const prompt = row[questionIndex]?.trim() ?? ""
    const options = optionIndexes.map((index) => row[index]?.trim() ?? "")
    while (options.length > 0 && options[options.length - 1] === "")
      options.pop()
    const rawAnswer = row[answerIndex]?.trim() ?? ""
    const letterAnswer = /^[a-f]$/i.test(rawAnswer)
      ? rawAnswer.toUpperCase().charCodeAt(0) - 65
      : NaN
    const numericAnswer = /^\d+$/.test(rawAnswer) ? Number(rawAnswer) : NaN
    const answer =
      Number.isInteger(letterAnswer) && letterAnswer >= 0
        ? letterAnswer
        : numericAnswer
    const rawPoints = pointsIndex < 0 ? "" : (row[pointsIndex]?.trim() ?? "")
    const points = rawPoints ? Number(rawPoints) : undefined
    const candidate = {
      question: prompt,
      options,
      answer,
      ...(points === undefined ? {} : { points }),
      ...(feedbackIndex < 0 ? {} : { feedback: row[feedbackIndex] ?? "" }),
    }
    const result = normaliseQuestion(candidate, `Row ${rowIndex + 2}`)
    if (result.question) questions.push(result.question)
    if (result.error) errors.push(result.error)
  })
  return { questions, errors }
}

export function parseQuestionImport(
  source: string,
  format: "json" | "csv"
): ImportResult {
  if (!source.trim())
    return { questions: [], errors: ["Paste JSON or CSV question data first."] }
  return format === "json"
    ? parseJsonQuestions(source)
    : parseCsvQuestions(source)
}
