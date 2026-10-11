import React from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"

import { ExamRunner } from "./quiz.$examId"

const apiState = vi.hoisted(() => ({
  saveAttempt: vi.fn(),
  getAttempt: vi.fn(),
  submitAnswers: vi.fn(),
  myResult: vi.fn(),
}))

vi.mock("@tanstack/react-router", async () => {
  const ReactModule = await import("react")
  return {
    createFileRoute: () => (config: Record<string, unknown>) => config,
    Link: (props: Record<string, unknown>) =>
      ReactModule.createElement(
        "a",
        { href: "#", ...props },
        props.children as React.ReactNode
      ),
  }
})
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({
    refetchQueries: vi.fn(() => Promise.resolve()),
    setQueryData: vi.fn(),
  }),
  useMutation: () => ({
    isPending: false,
    mutate: vi.fn(),
    mutateAsync: vi.fn(),
  }),
}))
vi.mock("@/lib/api", () => ({
  api: apiState,
  ApiError: class ApiError extends Error {
    status = 500
  },
}))
vi.mock("@/lib/auth", () => ({
  getAuth: () => ({ user: { user_id: "learner-qol" } }),
  useAuth: () => ({ user: { user_id: "learner-qol" } }),
}))
vi.mock("@/hooks/use-mounted", () => ({ useMounted: () => true }))
vi.mock("@/hooks/use-online-status", () => ({ useOnlineStatus: () => true }))
vi.mock("@/components/layout/app-shell", async () => {
  const ReactModule = await import("react")
  return {
    AppShell: ({ children }: { children: React.ReactNode }) =>
      ReactModule.createElement("main", null, children),
  }
})
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

describe("quiz runner review controls", () => {
  beforeEach(() => {
    cleanup()
    localStorage.clear()
    vi.clearAllMocks()
  })

  afterEach(cleanup)

  it("persists review flags, lets learners skip, and clear a selected answer", async () => {
    render(
      <ExamRunner
        examId="exam-qol"
        userId="learner-qol"
        title="Review skills"
        questions={[
          {
            index: 0,
            question: "First question?",
            options: ["First option", "Second option"],
          },
          {
            index: 1,
            question: "Second question?",
            options: ["Third option", "Fourth option"],
          },
          {
            index: 2,
            question: "Third question?",
            options: ["Fifth option", "Sixth option"],
          },
        ]}
      />
    )

    fireEvent.click(screen.getByRole("button", { name: "Flag for review" }))
    await waitFor(() =>
      expect(
        JSON.parse(
          localStorage.getItem("slate.quiz-progress.learner-qol.exam-qol.1")!
        ).flaggedIndices
      ).toEqual([0])
    )
    expect(
      screen.getByRole("button", { name: "Flagged for review" })
    ).toHaveAttribute("aria-pressed", "true")

    fireEvent.click(screen.getByRole("button", { name: "Skip question" }))
    expect(screen.getByText("Second question?")).toBeInTheDocument()
    fireEvent.click(screen.getByText("Fourth option"))
    await waitFor(() =>
      expect(screen.getByText("1/3 answered")).toBeInTheDocument()
    )
    fireEvent.click(screen.getByRole("button", { name: "Clear answer" }))
    await waitFor(() =>
      expect(screen.getByText("0/3 answered")).toBeInTheDocument()
    )
  })
})
