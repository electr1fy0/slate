import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { api } from "@/lib/api"
import { QuizPreviewDialog } from "./quiz-preview-dialog"
import { PresencePanel } from "./board/presence-panel"
import { EditExamDialog } from "./teacher/edit-exam-dialog"
import { ConfirmAction } from "./confirm-action"
import { Button } from "./ui/button"
import { safeRedirect } from "@/lib/navigation"
import { classCodeFromInput, classInviteUrl } from "./classroom-dashboard-real"

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))
beforeEach(() => vi.restoreAllMocks())

describe("teacher preview details", () => {
  it("shows weights, feedback and complete wrapping text", () => {
    render(
      <QuizPreviewDialog
        open
        onOpenChange={vi.fn()}
        title="Preview"
        questions={[
          {
            question: "Long\nprompt",
            options: ["Very long option", "Second"],
            answer: 0,
            points: 3,
            feedback: "Explanation",
          },
        ]}
      />
    )
    expect(screen.getByText("Question 1 · 3 points")).toBeInTheDocument()
    expect(screen.getByText("Feedback: Explanation")).toBeInTheDocument()
    expect(screen.getByText("Very long option")).not.toHaveClass("truncate")
    expect(screen.getByText(/Long.*prompt/)).toHaveClass("whitespace-pre-wrap")
  })
  it("announces loading and errors", () => {
    const view = render(
      <QuizPreviewDialog
        open
        onOpenChange={vi.fn()}
        title="Preview"
        questions={[]}
        loading
      />
    )
    expect(
      screen.getByRole("status", { name: "Loading quiz preview" })
    ).toBeInTheDocument()
    view.rerender(
      <QuizPreviewDialog
        open
        onOpenChange={vi.fn()}
        title="Preview"
        questions={[]}
        error="Unavailable"
      />
    )
    expect(screen.getByRole("alert")).toHaveTextContent("Unavailable")
  })
})

describe("presence and confirmations", () => {
  it("keeps Unicode initials intact and produces valid list children", () => {
    render(
      <PresencePanel
        selfName="😀A"
        selfRole="teacher"
        peers={[{ id: "peer", name: " ", role: "student", cursor: null }]}
      />
    )
    fireEvent.click(screen.getByRole("button", { name: "Participants (2)" }))
    expect(
      screen.getByText("😀A", { selector: "[data-slot=avatar-fallback]" })
    ).toBeInTheDocument()
    expect(screen.getByText("Participant")).toBeInTheDocument()
    expect(
      [...screen.getByRole("list").children].every(
        (node) => node.tagName === "LI"
      )
    ).toBe(true)
  })
  it("runs one action and keeps the pending dialog open", async () => {
    let reject!: (error: Error) => void
    const operation = vi.fn(
      () =>
        new Promise<void>((_done, fail) => {
          reject = fail
        })
    )
    render(
      <ConfirmAction
        title="Delete item?"
        description="This removes it."
        onConfirm={operation}
      >
        <Button>Remove</Button>
      </ConfirmAction>
    )
    fireEvent.click(screen.getByRole("button", { name: "Remove" }))
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }))
    fireEvent.click(screen.getByRole("button", { name: "Working…" }))
    expect(operation).toHaveBeenCalledOnce()
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled()
    await act(async () => reject(new Error("Try again")))
    expect(screen.getByRole("alert")).toHaveTextContent("Try again")
    expect(screen.getByRole("alertdialog")).toBeInTheDocument()
  })
})

describe("quiz settings continuity", () => {
  const exam = {
    id: "exam",
    title: "Original",
    question_count: 1,
    created_at: "2026-01-01T00:00:00Z",
    classroom_id: "missing-class",
  }
  it("retains edits across refreshes and shows an unavailable assignment", () => {
    const props = { classrooms: [], onOpenChange: vi.fn(), onSaved: vi.fn() }
    const view = render(<EditExamDialog exam={exam} {...props} />)
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "My edits" },
    })
    view.rerender(
      <EditExamDialog
        exam={{ ...exam, title: "Background refresh" }}
        {...props}
      />
    )
    expect(screen.getByLabelText("Title")).toHaveValue("My edits")
    expect(
      screen.getByRole("option", { name: "Assigned classroom unavailable" })
    ).toHaveValue("missing-class")
  })
  it("submits on Enter, blocks overlapping saves and ignores an old dialog's result", async () => {
    let resolve!: (value: typeof exam) => void
    const patch = vi.spyOn(api, "patchExam").mockReturnValue(
      new Promise((done) => {
        resolve = done
      })
    )
    const props = { classrooms: [], onOpenChange: vi.fn(), onSaved: vi.fn() }
    const view = render(<EditExamDialog exam={exam} {...props} />)
    fireEvent.keyDown(screen.getByLabelText("Title"), { key: "Enter" })
    fireEvent.keyDown(screen.getByLabelText("Title"), { key: "Enter" })
    expect(patch).toHaveBeenCalledOnce()
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled()
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" })
    expect(props.onOpenChange).not.toHaveBeenCalled()
    view.rerender(
      <EditExamDialog exam={{ ...exam, id: "different" }} {...props} />
    )
    await act(async () => resolve(exam))
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled()
    )
    expect(props.onSaved).not.toHaveBeenCalled()
    expect(props.onOpenChange).not.toHaveBeenCalled()
  })
})

describe("navigation inputs", () => {
  it.each([
    "//outside.example",
    "/\\outside.example",
    "/\noutside",
    "https://outside.example",
  ])("rejects unsafe local redirects %s", (target) => {
    expect(safeRedirect(target)).toBe("/")
  })
  it("preserves local return URLs and extracts invitation codes from links", () => {
    expect(safeRedirect("/?join_class=ABC234")).toBe("/?join_class=ABC234")
    expect(
      classCodeFromInput("https://school.example/?join_class=abc234")
    ).toBe("ABC234")
    expect(classCodeFromInput(" abc234 ")).toBe("ABC234")
    expect(classInviteUrl("ABC234", "https://school.example/")).toBe(
      "https://school.example/?join_class=ABC234"
    )
  })
})
