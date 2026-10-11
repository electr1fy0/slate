import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { ConfirmAction } from "./confirm-action"
import { Button } from "./ui/button"

describe("destructive confirmations", () => {
  it("does not run on opening or cancelling, and runs once on confirmation", async () => {
    const action = vi.fn()
    render(<ConfirmAction title="Delete this item?" description="Deletion is permanent." onConfirm={action}><Button>Delete item</Button></ConfirmAction>)
    fireEvent.click(screen.getByRole("button", { name: "Delete item" }))
    expect(screen.getByRole("alertdialog")).toBeVisible()
    expect(action).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole("button", { name: "Delete item" }))
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }))
    await waitFor(() => expect(action).toHaveBeenCalledTimes(1))
  })

  it("keeps errors visible for retry and disables controls during the operation", async () => {
    let reject: (cause: Error) => void = () => {}
    const action = vi.fn(() => new Promise((_resolve, fail) => { reject = fail }))
    render(<ConfirmAction title="Remove?" description="Confirm removal." onConfirm={action}><Button>Remove item</Button></ConfirmAction>)
    fireEvent.click(screen.getByRole("button", { name: "Remove item" }))
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }))
    expect(screen.getByRole("button", { name: "Working…" })).toBeDisabled()
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled()
    reject(new Error("Server unavailable"))
    expect(await screen.findByRole("alert")).toHaveTextContent("Server unavailable")
    expect(screen.getByRole("alertdialog")).toBeVisible()
    expect(screen.getByRole("button", { name: "Confirm" })).toBeEnabled()
  })
})
