import React from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { ApiError, api } from "@/lib/api"
import { clearAuth, setAuth } from "@/lib/auth"
import {
  removeQuestionFromBank,
  saveQuestionToBank,
  useQuestionBank,
} from "@/lib/question-bank"
import {
  removeQuizDraft,
  saveQuizDraft,
  useQuizDrafts,
} from "@/lib/quiz-drafts"
import {
  fromLocalDateTime,
  parseQuestionImport,
  toLocalDateTime,
} from "@/lib/teacher-authoring"
import type {
  ExamDraft,
  ExamMeta,
  ExamResultsResponse,
  QuestionDraft,
  QuizDraft,
} from "@/lib/types"
import { AnalyticsOverview, ExamManager, TeacherPage } from "./teacher"
import { QuizBuilder } from "@/components/teacher/quiz-builder"

const blockerHarness = vi.hoisted(() => ({
  shouldBlockFn: null as null | (() => boolean),
}))

vi.mock("@tanstack/react-router", async () => {
  const ReactModule = await import("react")
  return {
    createFileRoute: () => (config: Record<string, unknown>) => config,
    Link: (props: Record<string, unknown>) =>
      ReactModule.createElement(
        "a",
        { href: String(props.to ?? "#") },
        props.children as React.ReactNode
      ),
    useBlocker: (options: { shouldBlockFn: () => boolean }) => {
      blockerHarness.shouldBlockFn = options.shouldBlockFn
    },
  }
})

vi.mock("@/lib/guard", () => ({ requireTeacher: vi.fn() }))
vi.mock("@/components/layout/app-shell", () => ({
  AppShell: ({
    title,
    actions,
    children,
  }: {
    title: React.ReactNode
    actions: React.ReactNode
    children: React.ReactNode
  }) => (
    <main>
      <h1>{title}</h1>
      {actions}
      {children}
    </main>
  ),
}))

const teacher = {
  user_id: "teacher-qol-1",
  email: "teacher@example.test",
  name: "Teacher",
  role: "teacher" as const,
}
let signedInTeacher = teacher
let teacherSequence = 0
const queryClients = new Set<QueryClient>()
const question: QuestionDraft = {
  question: "What is two plus two?",
  options: ["3", "4", "5"],
  answer: 1,
  points: 3,
  feedback: "Add two and two.",
}

function exam(overrides: Partial<ExamMeta> = {}): ExamMeta {
  return {
    id: "exam-1",
    title: "Quiz one",
    question_count: 1,
    created_at: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

function serverDraft(overrides: Partial<QuizDraft> = {}): QuizDraft {
  return {
    id: "draft-1",
    owner_id: signedInTeacher.user_id,
    title: "Server draft",
    questions: [question],
    status: "draft",
    question_count: 1,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

function results(id: string): ExamResultsResponse {
  return {
    exam_id: id,
    count: 0,
    average_pct: null,
    submissions: [],
    question_stats: [],
  }
}

function renderWithQuery(ui: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  queryClients.add(client)
  return {
    ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>),
    client,
  }
}

function mockCoreQueries(): void {
  vi.spyOn(api, "listExams").mockResolvedValue([])
  vi.spyOn(api, "listQuizDrafts").mockResolvedValue([])
  vi.spyOn(api, "listClassrooms").mockResolvedValue([
    {
      id: "class-1",
      owner_id: signedInTeacher.user_id,
      name: "Physics 101",
      description: "Physics",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      member_count: 3,
    },
  ])
  vi.spyOn(api, "listQuestionBank").mockResolvedValue([])
}

beforeEach(() => {
  blockerHarness.shouldBlockFn = null
  Object.defineProperty(window, "confirm", {
    configurable: true,
    value: vi.fn(() => false),
  })
  localStorage.clear()
  clearAuth()
  signedInTeacher = {
    ...teacher,
    user_id: `${teacher.user_id}-${++teacherSequence}`,
  }
  setAuth({
    user: signedInTeacher,
    accessToken: "access",
    refreshToken: "refresh",
  })
  mockCoreQueries()
})

afterEach(async () => {
  cleanup()
  await Promise.all([...queryClients].map((client) => client.cancelQueries()))
  for (const client of queryClients) client.clear()
  queryClients.clear()
  vi.restoreAllMocks()
  clearAuth()
})

describe("teacher authoring helpers", () => {
  it("round-trips ISO times through the browser's local datetime control", () => {
    const original = "2027-04-05T13:45:00.000Z"
    expect(fromLocalDateTime(toLocalDateTime(original))).toBe(original)
    expect(toLocalDateTime(undefined)).toBe("")
    expect(fromLocalDateTime("")).toBeUndefined()
  })

  it("validates JSON and CSV imports while retaining answer, points, and feedback", () => {
    const json = parseQuestionImport(
      JSON.stringify({ questions: [question] }),
      "json"
    )
    expect(json.errors).toEqual([])
    expect(json.questions).toEqual([question])

    const csv = parseQuestionImport(
      'question,option_a,option_b,option_c,answer,points,feedback\n"What, exactly?",No,Yes,Maybe,B,5,"Use the comma carefully"',
      "csv"
    )
    expect(csv.errors).toEqual([])
    expect(csv.questions).toEqual([
      {
        question: "What, exactly?",
        options: ["No", "Yes", "Maybe"],
        answer: 1,
        points: 5,
        feedback: "Use the comma carefully",
      },
    ])
    const reordered = parseQuestionImport(
      "question,option_b,answer,option_a\nMapped answer,Second,A,First",
      "csv"
    )
    expect(reordered.errors).toEqual([])
    expect(reordered.questions).toEqual([
      { question: "Mapped answer", options: ["First", "Second"], answer: 0 },
    ])
    const reorderedNumeric = parseQuestionImport(
      "question,option_2,answer,option_1\nNumeric order,Second,0,First",
      "csv"
    )
    expect(reorderedNumeric.errors).toEqual([])
    expect(reorderedNumeric.questions[0]).toMatchObject({
      question: "Numeric order",
      options: ["First", "Second"],
      answer: 0,
    })
    expect(
      parseQuestionImport(
        "question,option_a,choice_a,option_b,answer\nDuplicate,A,Alias,B,A",
        "csv"
      ).errors[0]
    ).toContain("unique suffixes")
    expect(
      parseQuestionImport(
        "question,option_1,option_1,answer\nDuplicate,A,Alias,A",
        "csv"
      ).errors[0]
    ).toContain("unique suffixes")
    expect(
      parseQuestionImport(
        "question,option_a,option_2,answer\nMixed,A,B,A",
        "csv"
      ).errors[0]
    ).toContain("one scheme")
    expect(
      parseQuestionImport(
        "question,option_a,option_c,answer\nLetter gap,A,C,A",
        "csv"
      ).errors[0]
    ).toContain("contiguous")
    expect(
      parseQuestionImport(
        "question,option_1,option_3,answer\nNumeric gap,A,C,0",
        "csv"
      ).errors[0]
    ).toContain("contiguous")
    expect(
      parseQuestionImport(
        '[{"question":"Broken","options":["A","B"],"answer":4}]',
        "json"
      ).errors[0]
    ).toContain("answer must be")
    expect(
      parseQuestionImport(
        '[{"question":"Zero points","options":["A","B"],"answer":0,"points":0}]',
        "json"
      ).errors[0]
    ).toContain("points must be a whole number from 1 to 1000")
    expect(
      parseQuestionImport("question,option_a,answer\nPrompt,A,A", "csv")
        .errors[0]
    ).toContain("headers must include")
  })

  it("migrates legacy drafts once and scopes saved drafts and bank items to the signed-in teacher", () => {
    localStorage.setItem(
      "slate.quiz-drafts",
      JSON.stringify([
        {
          draft_id: "legacy-draft",
          title: "Legacy quiz",
          updated_at: "2026-01-01T00:00:00Z",
          questions: [question],
          classroom_id: "class-1",
          instructions: "Keep the scoring.",
        },
      ])
    )
    const hook = renderWithQuery(
      <>
        <DraftHook />
        <BankHook />
      </>
    )
    expect(screen.getByTestId("drafts")).toHaveTextContent("Legacy quiz")
    const scopedKey = `slate.quiz-drafts.${encodeURIComponent(signedInTeacher.user_id)}`
    expect(
      JSON.parse(localStorage.getItem(scopedKey) ?? "[]")[0]
    ).toMatchObject({
      classroom_id: "class-1",
      instructions: "Keep the scoring.",
      questions: [question],
    })
    expect(localStorage.getItem("slate.quiz-drafts")).toBeNull()

    act(() =>
      saveQuizDraft({
        title: "New quiz",
        questions: [question],
        instructions: "Retained",
        max_attempts: 2,
      })
    )
    expect(
      JSON.parse(localStorage.getItem(scopedKey) ?? "[]").some(
        (draft: { title: string }) => draft.title === "New quiz"
      )
    ).toBe(true)
    let savedBankId = ""
    act(() => {
      savedBankId = saveQuestionToBank(question).id
    })
    expect(screen.getByTestId("bank")).toHaveTextContent(question.question)
    expect(
      JSON.parse(
        localStorage.getItem(
          `slate.question-bank.${encodeURIComponent(signedInTeacher.user_id)}`
        ) ?? "[]"
      )[0]
    ).toMatchObject({
      points: 3,
      feedback: "Add two and two.",
    })
    act(() => removeQuizDraft("legacy-draft"))
    act(() => removeQuestionFromBank(savedBankId))
    hook.unmount()

    clearAuth()
    const otherTeacher = { ...signedInTeacher, user_id: "teacher-qol-other" }
    setAuth({ user: otherTeacher, accessToken: "other", refreshToken: "other" })
    const otherHook = renderWithQuery(
      <>
        <DraftHook />
        <BankHook />
      </>
    )
    expect(screen.getByTestId("drafts")).not.toHaveTextContent("New quiz")
    expect(screen.getByTestId("bank")).not.toHaveTextContent(question.question)
    otherHook.unmount()
  })
})

function DraftHook() {
  const drafts = useQuizDrafts()
  return (
    <output data-testid="drafts">
      {drafts.map((draft) => draft.title).join(",")}
    </output>
  )
}

function BankHook() {
  const bank = useQuestionBank()
  return (
    <output data-testid="bank">
      {bank.map((item) => item.question).join(",")}
    </output>
  )
}

function renderBuilder(initialDraft: ExamDraft | null = null) {
  return renderWithQuery(
    <QuizBuilder
      scopeId={signedInTeacher.user_id}
      initialDraft={initialDraft}
      onPublished={vi.fn()}
    />
  )
}

function namedQuestion(name: string): QuestionDraft {
  return {
    question: name,
    options: [`${name} A`, `${name} B`],
    answer: 1,
    points: 2,
    feedback: `${name} feedback`,
  }
}

describe("teacher authoring interactions", () => {
  it("names repeated question controls, exposes the selected answer and retains missing assignment", async () => {
    renderBuilder({
      title: "Accessible quiz",
      classroom_id: "missing-class",
      duration_minutes: 0,
      questions: [{ ...question, answer: 0 }, { ...question, answer: 0, question: "Another question" }],
    })
    expect(
      screen.getByRole("option", { name: "Assigned classroom unavailable" })
    ).toHaveValue("missing-class")
    expect(
      screen.getByRole("textbox", { name: "Question 2 prompt" })
    ).toHaveValue("Another question")
    expect(
      screen.getByRole("textbox", { name: "Question 2 option A" })
    ).toBeInTheDocument()
    expect(
      screen.getByRole("spinbutton", { name: "Question 2 points" })
    ).toBeInTheDocument()
    expect(
      screen.getByRole("textbox", { name: "Question 2 feedback" })
    ).toBeInTheDocument()
    const selected = screen.getByRole("button", {
      name: "Mark question 2 option A as correct",
    })
    expect(selected).toHaveAttribute("aria-pressed", "true")
    fireEvent.click(
      screen.getByRole("button", {
        name: "Mark question 2 option B as correct",
      })
    )
    expect(selected).toHaveAttribute("aria-pressed", "false")
    expect(
      screen.getByRole("button", { name: "Save question 2 to bank" })
    ).toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: "Add option to question 2" })
    ).toBeInTheDocument()
    const duration = screen.getByRole("spinbutton", {
      name: "Duration in minutes",
    })
    expect(duration).toHaveAttribute("min", "0")
    fireEvent.change(duration, { target: { value: "-1" } })
    expect(duration).toHaveAttribute("aria-invalid", "true")
    expect(screen.getByRole("button", { name: "Publish quiz" })).toBeDisabled()
  })
  it("saves assignment, timing, instructions, and scoring fields with a quiz draft", async () => {
    const update = vi
      .spyOn(api, "updateQuizDraft")
      .mockResolvedValue(serverDraft({ id: "saved-1" }))
    const seed = {
      id: "draft-1",
      title: "Seed quiz",
      questions: [{ question: "Seed?", options: ["A", "B"], answer: 0 }],
    }
    renderBuilder(seed)

    await screen.findByRole("option", { name: "Physics 101" })
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Unit check" },
    })
    fireEvent.change(screen.getByLabelText("Instructions (optional)"), {
      target: { value: "Read carefully." },
    })
    fireEvent.change(screen.getByLabelText("Assigned classroom"), {
      target: { value: "class-1" },
    })
    fireEvent.change(screen.getByLabelText("Opens at"), {
      target: { value: "2027-04-05T09:00" },
    })
    fireEvent.change(screen.getByLabelText("Closes at"), {
      target: { value: "2027-04-05T10:00" },
    })
    fireEvent.change(screen.getByLabelText("Duration in minutes"), {
      target: { value: "45" },
    })
    fireEvent.change(screen.getByLabelText("Maximum attempts"), {
      target: { value: "3" },
    })
    fireEvent.click(screen.getByLabelText("Accept late work"))
    fireEvent.change(screen.getByLabelText("Late grace in minutes"), {
      target: { value: "15" },
    })
    fireEvent.change(screen.getByLabelText("Prompt"), {
      target: { value: "What is 2 + 2?" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option A"), {
      target: { value: "3" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option B"), {
      target: { value: "4" },
    })
    fireEvent.click(
      screen.getByLabelText("Mark question 1 option B as correct")
    )
    fireEvent.change(screen.getByLabelText("Points"), {
      target: { value: "7" },
    })
    fireEvent.change(screen.getByLabelText("Feedback (optional)"), {
      target: { value: "Add both numbers." },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))

    await waitFor(() => expect(update).toHaveBeenCalled())
    expect(update).toHaveBeenCalledWith(
      "draft-1",
      expect.objectContaining({
        title: "Unit check",
        instructions: "Read carefully.",
        classroom_id: "class-1",
        opens_at: fromLocalDateTime("2027-04-05T09:00"),
        closes_at: fromLocalDateTime("2027-04-05T10:00"),
        duration_minutes: 45,
        max_attempts: 3,
        allow_late: true,
        late_grace_minutes: 15,
        questions: [
          {
            question: "What is 2 + 2?",
            options: ["3", "4"],
            answer: 1,
            points: 7,
            feedback: "Add both numbers.",
          },
        ],
      })
    )
  }, 15_000)

  it("previews validated JSON imports and reorders questions without losing scoring", async () => {
    const seed = {
      id: "draft-1",
      title: "Ordering",
      questions: [
        {
          question: "First",
          options: ["A", "B", "C"],
          answer: 2,
          points: 3,
          feedback: "first feedback",
        },
        {
          question: "Second",
          options: ["D", "E", "F"],
          answer: 2,
          points: 8,
          feedback: "second feedback",
        },
      ],
    }
    const update = vi
      .spyOn(api, "updateQuizDraft")
      .mockResolvedValue(serverDraft({ id: "draft-1" }))
    const builderRender = renderBuilder(seed)

    fireEvent.click(screen.getByRole("button", { name: "Move question 2 up" }))
    expect(
      screen
        .getAllByLabelText("Prompt")
        .map((element) => (element as HTMLTextAreaElement).value)
    ).toEqual(["Second", "First"])
    fireEvent.click(
      screen.getByRole("button", { name: "Move question 1 down" })
    )
    expect(
      screen
        .getAllByLabelText("Prompt")
        .map((element) => (element as HTMLTextAreaElement).value)
    ).toEqual(["First", "Second"])
    fireEvent.click(screen.getByRole("button", { name: "Move question 2 up" }))
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(update).toHaveBeenCalled())
    expect(update.mock.calls.at(-1)?.[1].questions).toEqual(
      [...seed.questions].reverse()
    )
    builderRender.unmount()

    const importRender = renderBuilder(null)
    fireEvent.change(screen.getByLabelText("Question data"), {
      target: { value: JSON.stringify([question]) },
    })
    fireEvent.click(screen.getByRole("button", { name: "Preview import" }))
    expect(
      await screen.findByText("Ready to import 1 question")
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Add 1 question" }))
    expect(screen.getByLabelText("Prompt")).toHaveValue(question.question)
    expect(screen.getByLabelText("Points")).toHaveValue(3)
    importRender.unmount()
  })

  it("keeps an existing zero max-attempt value when loading and resaving a draft", async () => {
    const update = vi
      .spyOn(api, "updateQuizDraft")
      .mockResolvedValue(serverDraft({ id: "draft-1" }))
    renderBuilder({
      id: "draft-1",
      title: "Unlimited",
      max_attempts: 0,
      questions: [question],
    })

    expect(screen.getByLabelText("Maximum attempts")).toHaveValue(0)
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() =>
      expect(update).toHaveBeenCalledWith(
        "draft-1",
        expect.objectContaining({ max_attempts: 0 })
      )
    )
  })

  it("confirms option removal and preserves the correct answer index", async () => {
    const seed = {
      id: "draft-1",
      title: "Remove option",
      questions: [
        { question: "Pick C", options: ["A", "B", "C"], answer: 2, points: 4 },
      ],
    }
    const update = vi
      .spyOn(api, "updateQuizDraft")
      .mockResolvedValue(serverDraft({ id: "draft-1" }))
    renderBuilder(seed)
    fireEvent.click(
      screen.getByRole("button", { name: "Remove question 1 option B" })
    )
    expect(update).not.toHaveBeenCalled()
    fireEvent.click(
      await screen.findByRole("button", { name: "Remove option" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(update).toHaveBeenCalled())
    expect(update.mock.calls.at(-1)?.[1].questions[0]).toMatchObject({
      options: ["A", "C"],
      answer: 1,
      points: 4,
    })
  })

  it("rejects incomplete questions before saving them to the question bank", async () => {
    const createBankQuestion = vi
      .spyOn(api, "createBankQuestion")
      .mockResolvedValue({
        id: "bank-1",
        saved_at: "2026-01-01T00:00:00Z",
        ...question,
      })
    renderBuilder()
    fireEvent.change(screen.getByLabelText("Prompt"), {
      target: { value: "Incomplete" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option A"), {
      target: { value: "A" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option B"), {
      target: { value: "B" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: /^Add option to question / })
    )
    fireEvent.click(
      screen.getByRole("button", { name: /^Save question \d+ to bank$/ })
    )

    expect(createBankQuestion).not.toHaveBeenCalled()
  })

  it("queues newer edits behind an in-flight server save instead of overwriting them", async () => {
    const seed = { id: "draft-1", title: "Seed quiz", questions: [question] }
    let resolveFirst: (draft: QuizDraft) => void = () => undefined
    const firstResponse = new Promise<QuizDraft>((resolve) => {
      resolveFirst = resolve
    })
    const update = vi
      .spyOn(api, "updateQuizDraft")
      .mockImplementationOnce(() => firstResponse)
      .mockResolvedValue(serverDraft({ id: "draft-1" }))
    renderBuilder(seed)

    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Older edit" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Newest edit" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    expect(update).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveFirst(serverDraft({ id: "draft-1" }))
    })
    await waitFor(() => expect(update).toHaveBeenCalledTimes(2))
    expect(update.mock.calls[0]?.[1].title).toBe("Older edit")
    expect(update.mock.calls[1]?.[1].title).toBe("Newest edit")
  })

  it("drops queued server writes when the signed-in teacher changes", async () => {
    let resolveFirst: ((draft: QuizDraft) => void) | null = null
    const firstResponse = new Promise<QuizDraft>((resolve) => {
      resolveFirst = resolve
    })
    const update = vi
      .spyOn(api, "updateQuizDraft")
      .mockImplementationOnce(() => firstResponse)
      .mockResolvedValue(serverDraft({ id: "draft-1" }))
    renderBuilder({ id: "draft-1", title: "Seed quiz", questions: [question] })

    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Older edit" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Queued old edit" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))

    const nextTeacher = { ...teacher, user_id: "different-teacher" }
    act(() =>
      setAuth({
        user: nextTeacher,
        accessToken: "other-access",
        refreshToken: "other-refresh",
      })
    )
    await act(async () => {
      resolveFirst?.(serverDraft({ id: "draft-1" }))
    })

    expect(update).toHaveBeenCalledTimes(1)
    expect(
      localStorage.getItem(
        `slate.quiz-drafts.${encodeURIComponent(nextTeacher.user_id)}`
      )
    ).toBeNull()
  })

  it("does not recreate a draft after an old-account 404 arrives", async () => {
    let rejectFirst: ((error: unknown) => void) | null = null
    const firstResponse = new Promise<QuizDraft>((_resolve, reject) => {
      rejectFirst = reject
    })
    const update = vi
      .spyOn(api, "updateQuizDraft")
      .mockImplementationOnce(() => firstResponse)
    const create = vi
      .spyOn(api, "createQuizDraft")
      .mockResolvedValue(serverDraft({ id: "recreated" }))
    renderBuilder({ id: "draft-1", title: "Seed quiz", questions: [question] })

    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Pending save" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Queued save" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))

    const nextTeacher = { ...teacher, user_id: "different-teacher" }
    act(() =>
      setAuth({
        user: nextTeacher,
        accessToken: "other-access",
        refreshToken: "other-refresh",
      })
    )
    await act(async () => {
      rejectFirst?.(new ApiError("old draft missing", 404))
    })

    expect(create).not.toHaveBeenCalled()
    expect(update).toHaveBeenCalledTimes(1)
    expect(
      localStorage.getItem(
        `slate.quiz-drafts.${encodeURIComponent(nextTeacher.user_id)}`
      )
    ).toBeNull()
  })

  it("writes the latest edit during before-unload", async () => {
    const renderResult = renderBuilder(null)
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "First edit" },
    })
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent("Changes not synced")
    )
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Latest edit" },
    })
    const unload = new Event("beforeunload", { cancelable: true })
    act(() => {
      window.dispatchEvent(unload)
    })
    const stored = JSON.parse(
      localStorage.getItem(
        `slate.quiz-drafts.${encodeURIComponent(signedInTeacher.user_id)}`
      ) ?? "[]"
    )
    expect(stored[0].title).toBe("Latest edit")
    expect(unload.defaultPrevented).toBe(true)
    renderResult.unmount()
  })

  it("blocks SPA navigation until confirmed and flushes the current local draft", async () => {
    renderWithQuery(<TeacherPage />)
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Navigation draft" },
    })
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent("Changes not synced")
    )
    const confirm = vi.mocked(window.confirm).mockReturnValue(false)
    expect(blockerHarness.shouldBlockFn?.()).toBe(true)
    expect(confirm).toHaveBeenCalled()

    confirm.mockReturnValue(true)
    expect(blockerHarness.shouldBlockFn?.()).toBe(false)
    const stored = JSON.parse(
      localStorage.getItem(
        `slate.quiz-drafts.${encodeURIComponent(signedInTeacher.user_id)}`
      ) ?? "[]"
    )
    expect(stored[0].title).toBe("Navigation draft")
  })

  it("confirms before loading a different local draft", async () => {
    act(() => {
      saveQuizDraft(
        { title: "Saved quiz", questions: [question] },
        "saved-local"
      )
    })
    renderBuilder(null)
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Unsaved quiz" },
    })
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent("Changes not synced")
    )
    const confirm = vi.mocked(window.confirm).mockReturnValue(false)
    fireEvent.click(
      screen.getByRole("button", { name: /^Load (local|workspace) draft / })
    )
    expect(screen.getByLabelText("Title")).toHaveValue("Unsaved quiz")
    expect(confirm).toHaveBeenCalled()
    confirm.mockReturnValue(true)
    fireEvent.click(
      screen.getByRole("button", { name: /^Load (local|workspace) draft / })
    )
    expect(screen.getByLabelText("Title")).toHaveValue("Saved quiz")
    expect(screen.getByLabelText("Prompt")).toHaveValue(question.question)
  })
})

describe("teacher exam management and analytics", () => {
  it("filters and searches quizzes, then confirms close and delete before calling the API", async () => {
    vi.mocked(api.listExams).mockResolvedValue([
      exam({
        id: "open-quiz",
        title: "Algebra review",
        closes_at: "2027-04-05T10:00:00Z",
      }),
      exam({
        id: "closed-quiz",
        title: "Archived algebra",
        closes_at: "2025-01-01T10:00:00Z",
      }),
      exam({
        id: "closed-biology",
        title: "Biology review",
        closes_at: "2025-01-01T10:00:00Z",
      }),
    ])
    const close = vi
      .spyOn(api, "closeExam")
      .mockResolvedValue({ id: "open-quiz", closed_at: "2026-01-01T00:00:00Z" })
    const deleteExam = vi.spyOn(api, "deleteExam").mockResolvedValue(undefined)
    renderWithQuery(
      <ExamManager scopeId={signedInTeacher.user_id} onDuplicate={vi.fn()} />
    )

    await screen.findByText("Algebra review")
    fireEvent.change(screen.getByLabelText("Search quizzes"), {
      target: { value: "algebra" },
    })
    fireEvent.change(screen.getByLabelText("Filter quizzes by status"), {
      target: { value: "closed" },
    })
    expect(screen.getByText("Archived algebra")).toBeInTheDocument()
    expect(screen.queryByText("Algebra review")).not.toBeInTheDocument()
    expect(screen.queryByText("Biology review")).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText("Filter quizzes by status"), {
      target: { value: "all" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    expect(close).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole("button", { name: "Close quiz" }))
    await waitFor(() => expect(close).toHaveBeenCalledWith("open-quiz"))

    fireEvent.click(
      screen.getByRole("button", { name: "Delete Archived algebra" })
    )
    expect(deleteExam).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole("button", { name: "Delete quiz" }))
    await waitFor(() => expect(deleteExam).toHaveBeenCalledWith("closed-quiz"))
  })

  it("edits published settings and clears a scheduled close to reopen the quiz", async () => {
    vi.mocked(api.listExams).mockResolvedValue([
      exam({
        id: "scheduled",
        title: "Scheduled quiz",
        opens_at: "2027-04-05T09:00:00.000Z",
        closes_at: "2027-04-05T10:00:00.000Z",
        duration_minutes: 45,
        max_attempts: 3,
        allow_late: true,
        late_grace_minutes: 15,
        classroom_id: "class-1",
      }),
    ])
    const patchExam = vi
      .spyOn(api, "patchExam")
      .mockResolvedValue(
        exam({ id: "scheduled", title: "Reopened quiz", closes_at: "" })
      )
    renderWithQuery(
      <ExamManager scopeId={signedInTeacher.user_id} onDuplicate={vi.fn()} />
    )
    await screen.findByText("Scheduled quiz")
    fireEvent.click(screen.getByRole("button", { name: "Edit" }))

    fireEvent.change(await screen.findByLabelText("Title"), {
      target: { value: "Reopened quiz" },
    })
    fireEvent.change(screen.getByLabelText("Closes at"), {
      target: { value: "" },
    })
    fireEvent.change(screen.getByLabelText("Duration in minutes"), {
      target: { value: "" },
    })
    fireEvent.change(screen.getByLabelText("Maximum attempts"), {
      target: { value: "" },
    })
    fireEvent.change(screen.getByLabelText("Late grace in minutes"), {
      target: { value: "" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    await waitFor(() =>
      expect(patchExam).toHaveBeenCalledWith(
        "scheduled",
        expect.objectContaining({
          title: "Reopened quiz",
          classroom_id: "class-1",
          opens_at: "2027-04-05T09:00:00.000Z",
          closes_at: "",
          duration_minutes: 0,
          max_attempts: 1,
          allow_late: true,
          late_grace_minutes: 0,
        })
      )
    )
  })

  it("preserves an existing unlimited-attempt setting when editing a published quiz", async () => {
    vi.mocked(api.listExams).mockResolvedValue([
      exam({ id: "unlimited", title: "Unlimited quiz", max_attempts: 0 }),
    ])
    const patchExam = vi
      .spyOn(api, "patchExam")
      .mockResolvedValue(
        exam({ id: "unlimited", title: "Unlimited quiz", max_attempts: 0 })
      )
    renderWithQuery(
      <ExamManager scopeId={signedInTeacher.user_id} onDuplicate={vi.fn()} />
    )
    await screen.findByText("Unlimited quiz")
    fireEvent.click(screen.getByRole("button", { name: "Edit" }))

    expect(await screen.findByLabelText("Maximum attempts")).toHaveValue(0)
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    await waitFor(() =>
      expect(patchExam).toHaveBeenCalledWith(
        "unlimited",
        expect.objectContaining({ max_attempts: 0 })
      )
    )
  })

  it("lets teachers select which quiz analytics displays and uses a scoped result query", async () => {
    vi.mocked(api.listExams).mockResolvedValue([
      exam({ id: "first", title: "First quiz" }),
      exam({ id: "second", title: "Second quiz", question_count: 2 }),
    ])
    const examResults = vi
      .spyOn(api, "examResults")
      .mockImplementation(async (id) => ({
        ...results(id),
        count: id === "second" ? 2 : 1,
        average_pct: id === "second" ? 87 : 32,
      }))
    const { client } = renderWithQuery(
      <AnalyticsOverview scopeId={signedInTeacher.user_id} />
    )
    await screen.findByLabelText("Choose quiz for analytics")
    fireEvent.change(screen.getByLabelText("Choose quiz for analytics"), {
      target: { value: "second" },
    })
    await waitFor(() => expect(examResults).toHaveBeenCalledWith("second"))
    expect((await screen.findAllByText("87%")).length).toBeGreaterThan(0)
    expect(
      client
        .getQueryCache()
        .find({ queryKey: ["exam-results", signedInTeacher.user_id, "second"] })
    ).toBeDefined()
  })
})

describe("draft and question-bank browsing", () => {
  it("searches and paginates all local, server, and question-bank items", async () => {
    const remoteDrafts = Array.from({ length: 6 }, (_, index) =>
      serverDraft({ id: `server-${index}`, title: `Server draft ${index}` })
    )
    vi.mocked(api.listQuizDrafts).mockResolvedValue(remoteDrafts)
    vi.mocked(api.listQuestionBank).mockResolvedValue([
      {
        ...namedQuestion("Remote bank question"),
        id: "bank-remote",
        saved_at: "2026-01-01T00:00:00Z",
      },
    ])
    act(() => {
      for (let index = 0; index < 6; index += 1) {
        saveQuizDraft(
          {
            title: `Local quiz ${index}`,
            questions: [namedQuestion(`Local question ${index}`)],
          },
          `local-${index}`
        )
        saveQuestionToBank(namedQuestion(`Local bank question ${index}`))
      }
    })
    expect(await api.listQuizDrafts()).toEqual(remoteDrafts)
    renderBuilder(null)

    await screen.findByText("Local quiz 5")
    expect(await screen.findByText("Server draft 0")).toBeInTheDocument()
    expect(await screen.findByText("Remote bank question")).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole("button", { name: "Next" })[0])
    expect(screen.getByText("Local quiz 0")).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText("Search local drafts"), {
      target: { value: "Local quiz 2" },
    })
    expect(screen.getByText("Local quiz 2")).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Search server drafts"), {
      target: { value: "Server draft 5" },
    })
    expect(screen.getByText("Server draft 5")).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Search question bank"), {
      target: { value: "Local bank question 0" },
    })
    expect(screen.getByText("Local bank question 0")).toBeInTheDocument()
  })

  it("confirms local draft and bank deletion before removing saved data", async () => {
    act(() => {
      saveQuizDraft(
        { title: "Keep or delete", questions: [question] },
        "local-delete"
      )
      saveQuestionToBank(namedQuestion("Delete from bank"))
    })
    renderBuilder(null)
    fireEvent.click(
      screen.getByRole("button", { name: "Delete Keep or delete" })
    )
    expect(
      JSON.parse(
        localStorage.getItem(
          `slate.quiz-drafts.${encodeURIComponent(signedInTeacher.user_id)}`
        ) ?? "[]"
      )
    ).toHaveLength(1)
    fireEvent.click(await screen.findByRole("button", { name: "Delete draft" }))
    await waitFor(() =>
      expect(
        JSON.parse(
          localStorage.getItem(
            `slate.quiz-drafts.${encodeURIComponent(signedInTeacher.user_id)}`
          ) ?? "[]"
        )
      ).toHaveLength(0)
    )

    fireEvent.click(
      screen.getByRole("button", {
        name: "Delete bank question Delete from bank",
      })
    )
    fireEvent.click(
      await screen.findByRole("button", { name: "Delete question" })
    )
    await waitFor(() =>
      expect(screen.queryByText("Delete from bank")).not.toBeInTheDocument()
    )
  })
})
