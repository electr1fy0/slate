import { act, fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupTextarea,
} from "./ui/input-group"
import { QuestionImport } from "./teacher/question-import"
import { ConfirmAction } from "./confirm-action"
import { Button } from "./ui/button"

describe("composed input groups", () => {
  it("focuses a textarea after running the supplied addon handler", () => {
    const click = vi.fn()
    render(
      <InputGroup>
        <InputGroupTextarea aria-label="Notes" />
        <InputGroupAddon onClick={click}>Words</InputGroupAddon>
      </InputGroup>
    )
    fireEvent.click(screen.getByText("Words"))
    expect(click).toHaveBeenCalledOnce()
    expect(screen.getByRole("textbox", { name: "Notes" })).toHaveFocus()
  })
  it("respects an addon handler that prevents focus", () => {
    render(
      <InputGroup>
        <InputGroupTextarea aria-label="Notes" />
        <InputGroupAddon onClick={(event) => event.preventDefault()}>
          Words
        </InputGroupAddon>
      </InputGroup>
    )
    fireEvent.click(screen.getByText("Words"))
    expect(screen.getByRole("textbox", { name: "Notes" })).not.toHaveFocus()
  })
})

describe("import review", () => {
  it("exposes separate choices and imports one reviewed batch once", () => {
    const imported = vi.fn()
    render(<QuestionImport onImport={imported} />)
    const field = screen.getByRole("textbox", { name: "Question data" })
    expect(field).toHaveAccessibleDescription(/array of objects/)
    fireEvent.change(field, {
      target: {
        value: JSON.stringify([
          {
            question: "Line one\nLine two",
            options: ["First · choice", "Second\nchoice"],
            answer: 0,
            feedback: "Explain\nwhy",
          },
        ]),
      },
    })
    fireEvent.click(screen.getByRole("button", { name: "Preview import" }))
    expect(screen.getAllByRole("listitem")).toHaveLength(2)
    const add = screen.getByRole("button", { name: "Add 1 question" })
    act(() => {
      add.click()
      add.click()
    })
    expect(imported).toHaveBeenCalledOnce()
  })
  it("discards the old preview after source edits", () => {
    render(<QuestionImport onImport={vi.fn()} />)
    const field = screen.getByRole("textbox", { name: "Question data" })
    fireEvent.change(field, {
      target: { value: '[{"question":"Q","options":["A","B"],"answer":0}]' },
    })
    fireEvent.click(screen.getByRole("button", { name: "Preview import" }))
    fireEvent.change(field, { target: { value: "different input" } })
    expect(
      screen.queryByRole("button", { name: "Add 1 question" })
    ).not.toBeInTheDocument()
  })
})

it("announces a pending confirmation and restores actionable errors", async () => {
  let fail!: (error: Error) => void
  const onConfirm = () =>
    new Promise<void>((_resolve, reject) => {
      fail = reject
    })
  render(
    <ConfirmAction
      title="Delete item?"
      description="Deletes the item"
      onConfirm={onConfirm}
    >
      <Button>Delete</Button>
    </ConfirmAction>
  )
  fireEvent.click(screen.getByRole("button", { name: "Delete" }))
  fireEvent.click(screen.getByRole("button", { name: "Confirm" }))
  expect(screen.getByRole("alertdialog")).toHaveAttribute("aria-busy", "true")
  await act(async () => fail(new Error("Please retry")))
  expect(screen.getByRole("alert")).toHaveTextContent("Please retry")
  expect(screen.getByRole("alertdialog")).toHaveAttribute("aria-busy", "false")
})
