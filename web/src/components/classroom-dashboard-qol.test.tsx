import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"

import { clearAuth, setAuth } from "@/lib/auth"
import { api } from "@/lib/api"
import { ClassroomDashboard } from "./classroom-dashboard"

const apiState = vi.hoisted(() => ({
  listClassrooms: vi.fn(),
  createClassroom: vi.fn(),
  joinClassroom: vi.fn(),
  classroomRoster: vi.fn(),
  classroomInvitations: vi.fn(),
  inviteClassroomMember: vi.fn(),
  deleteClassroomInvitation: vi.fn(),
  removeClassroomMember: vi.fn(),
  updateClassroom: vi.fn(),
  deleteClassroom: vi.fn(),
  leaveClassroom: vi.fn(),
}))

vi.mock("@/lib/api", () => ({ api: apiState }))
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const classroom = (id: string, name: string) => ({
  id,
  owner_id: "teacher-1",
  name,
  description: "Algebra",
  join_code: "ABC234",
  created_at: "2026-10-01T10:00:00.000Z",
  updated_at: "2026-10-01T10:00:00.000Z",
  member_count: 1,
})

const invite = {
  id: "invitation-1",
  classroom_id: "class-1",
  name: "Lin Chen",
  email: "lin@example.com",
  status: "pending" as const,
  created_at: "2026-10-02T10:00:00.000Z",
  join_code: "INV234",
  delivery: "not_configured" as const,
}

describe("classroom dashboard workflows", () => {
  beforeEach(() => {
    cleanup()
    localStorage.clear()
    clearAuth()
    Object.values(apiState).forEach((method) => method.mockReset())
    apiState.listClassrooms.mockResolvedValue([])
    apiState.classroomRoster.mockResolvedValue([])
    apiState.classroomInvitations.mockResolvedValue([])
  })

  afterEach(cleanup)

  it("joins through the server and only then adds the class to the learner workspace", async () => {
    setAuth({
      user: {
        user_id: "student-1",
        email: "sam@example.com",
        name: "Sam Lee",
        role: "student",
      },
      accessToken: "access",
      refreshToken: "refresh",
    })
    apiState.joinClassroom.mockResolvedValue(
      classroom("joined-class", "Algebra 1")
    )
    render(<ClassroomDashboard />)

    fireEvent.click(
      await screen.findByRole("button", { name: "Join with code" })
    )
    fireEvent.change(screen.getByLabelText("Class or invitation code"), {
      target: { value: "abc234" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Join class" }))

    await waitFor(() =>
      expect(api.joinClassroom).toHaveBeenCalledWith("ABC234")
    )
    expect(await screen.findByText("Algebra 1")).toBeInTheDocument()
    expect(api.joinClassroom).toHaveBeenCalledTimes(1)
  })

  it("creates a server invitation and reports unavailable email delivery honestly", async () => {
    setAuth({
      user: {
        user_id: "teacher-1",
        email: "teacher@example.com",
        name: "Ada",
        role: "teacher",
      },
      accessToken: "access",
      refreshToken: "refresh",
    })
    apiState.listClassrooms.mockResolvedValue([
      classroom("class-1", "Algebra 1"),
    ])
    apiState.classroomRoster.mockResolvedValue([
      {
        classroom_id: "class-1",
        user_id: "teacher-1",
        name: "Ada",
        email: "teacher@example.com",
        role: "teacher",
        joined_at: "2026-10-01T10:00:00.000Z",
      },
    ])
    apiState.inviteClassroomMember.mockResolvedValue(invite)
    render(<ClassroomDashboard />)

    fireEvent.click(
      await screen.findByRole("button", { name: "Invite learner" })
    )
    fireEvent.change(screen.getByLabelText("Learner name"), {
      target: { value: "Lin Chen" },
    })
    fireEvent.change(screen.getByLabelText("Learner email"), {
      target: { value: "lin@example.com" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Create invitation" }))

    await waitFor(() =>
      expect(api.inviteClassroomMember).toHaveBeenCalledWith("class-1", {
        name: "Lin Chen",
        email: "lin@example.com",
      })
    )
    expect(await screen.findByText("email not configured")).toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: "Copy invitation link" })
    ).toBeInTheDocument()
    expect(screen.queryByText("Invitation email sent")).not.toBeInTheDocument()
  })
})
