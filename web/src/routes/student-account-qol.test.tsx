import React from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"

import { api } from "@/lib/api"
import { clearAuth, getAuth, setAuth } from "@/lib/auth"
import { AccountSettingsPage } from "./settings"
import { LoginPage } from "./login"
import { ExamLobby, ResultView } from "./quiz.$examId"
import { toast } from "sonner"

const state = vi.hoisted(() => ({
  search: {},
  examInfo: {
    id: "exam-1",
    title: "Midterm",
    question_count: 4,
    created_at: "2026-10-01T10:00:00.000Z",
    status: "published",
    instructions: "Read each question carefully.",
    duration_minutes: 25,
    attempt_status: "not_started",
    max_attempts: 2,
    allow_late: true,
    late_grace_minutes: 10,
  },
  examRequested: false,
  navigate: vi.fn(() => Promise.resolve()),
  api: {
    login: vi.fn(),
    register: vi.fn(),
    updateProfile: vi.fn(),
    changePassword: vi.fn(),
    forgotPassword: vi.fn(),
    resetPassword: vi.fn(),
    examInfo: vi.fn(),
    getExam: vi.fn(),
    retakeExam: vi.fn(),
  },
}))

vi.mock("@tanstack/react-router", async () => {
  const ReactModule = await import("react")
  return {
    createFileRoute: () => (config: Record<string, unknown>) => ({
      ...config,
      useSearch: () => state.search,
      useParams: () => ({ examId: "exam-1" }),
    }),
    useNavigate: () => state.navigate,
    Link: (props: Record<string, unknown>) =>
      ReactModule.createElement(
        "a",
        { href: typeof props.to === "string" ? props.to : "#", ...props },
        props.children as React.ReactNode
      ),
  }
})

vi.mock("@tanstack/react-query", async () => {
  const ReactModule = await import("react")
  return {
    useQueryClient: () => ({
      invalidateQueries: vi.fn(() => Promise.resolve()),
      removeQueries: vi.fn(),
      refetchQueries: vi.fn(() => Promise.resolve()),
      setQueryData: vi.fn(),
    }),
    useQuery: (options: {
      queryKey?: readonly unknown[]
      enabled?: boolean
      queryFn?: () => unknown
    }) => {
      const key = String(options.queryKey?.[0] ?? "")
      ReactModule.useEffect(() => {
        if (key === "exam" && options.enabled && !state.examRequested) {
          state.examRequested = true
          void Promise.resolve(options.queryFn?.())
        }
      }, [key, options.enabled])
      if (key === "exam-info")
        return { data: state.examInfo, isPending: false, isError: false }
      if (key === "exam")
        return {
          data: undefined,
          isPending: false,
          isError: false,
          isFetching: Boolean(options.enabled),
        }
      return {
        data: undefined,
        isPending: false,
        isError: false,
        isFetching: false,
      }
    },
  }
})

vi.mock("@/lib/api", () => {
  class MockApiError extends Error {
    status: number
    code?: string
    constructor(message: string, status: number, code?: string) {
      super(message)
      this.status = status
      this.code = code
    }
  }
  return { api: state.api, ApiError: MockApiError }
})

vi.mock("@/components/layout/app-shell", async () => {
  const ReactModule = await import("react")
  return {
    AppShell: ({
      title,
      children,
    }: {
      title: string
      children: React.ReactNode
    }) =>
      ReactModule.createElement(
        "main",
        null,
        ReactModule.createElement("h1", null, title),
        children
      ),
  }
})

vi.mock("@/lib/guard", () => ({ requireAuth: vi.fn() }))
vi.mock("@/hooks/use-mounted", () => ({ useMounted: () => true }))
vi.mock("@/hooks/use-online-status", () => ({ useOnlineStatus: () => true }))
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const session = {
  user: {
    user_id: "student-1",
    email: "ada@example.com",
    name: "Ada",
    role: "student" as const,
  },
  accessToken: "access-1",
  refreshToken: "refresh-1",
}

describe("student account and assessment entry flows", () => {
  beforeEach(() => {
    cleanup()
    localStorage.clear()
    clearAuth()
    setAuth(session)
    state.search = {}
    state.examRequested = false
    state.navigate.mockClear()
    Object.values(state.api).forEach((method) => method.mockReset())
    state.api.updateProfile.mockResolvedValue({
      ...session.user,
      name: "Ada Lovelace",
    })
    state.api.changePassword.mockResolvedValue({ message: "updated" })
    state.api.forgotPassword.mockResolvedValue({
      message: "generic response",
      delivery: "not_configured",
    })
    state.api.resetPassword.mockResolvedValue({ message: "updated" })
    state.api.login.mockResolvedValue({
      ...session,
      access_token: "access-1",
      refresh_token: "refresh-1",
    })
    state.api.getExam.mockResolvedValue({
      id: "exam-1",
      title: "Midterm",
      question_count: 4,
      questions: [],
    })
    state.api.retakeExam.mockResolvedValue({
      answers: {},
      version: 0,
      attempt_number: 2,
    })
  })

  afterEach(cleanup)

  it("updates the profile and signs out after changing the password", async () => {
    render(<AccountSettingsPage />)
    fireEvent.change(screen.getByLabelText("Display name"), {
      target: { value: "Ada Lovelace" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save profile" }))
    await waitFor(() =>
      expect(api.updateProfile).toHaveBeenCalledWith({ name: "Ada Lovelace" })
    )
    expect(getAuth()).toMatchObject({
      user: { name: "Ada Lovelace" },
      accessToken: "access-1",
      refreshToken: "refresh-1",
    })

    fireEvent.change(screen.getByLabelText("Current password"), {
      target: { value: "old-password" },
    })
    fireEvent.change(screen.getByLabelText("New password"), {
      target: { value: "new-password" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: "Show current password" })
    )
    expect(screen.getByLabelText("Current password")).toHaveAttribute(
      "type",
      "text"
    )
    fireEvent.click(screen.getByRole("button", { name: "Change password" }))
    await waitFor(() =>
      expect(api.changePassword).toHaveBeenCalledWith({
        current_password: "old-password",
        password: "new-password",
      })
    )
    await waitFor(() => {
      expect(getAuth().user).toBeNull()
      expect(state.navigate).toHaveBeenCalledWith({ to: "/login" })
    })
    expect(localStorage.getItem("slate.auth")).toBeNull()
    expect(toast.success).toHaveBeenCalledWith(
      "Password changed. Sign in again with your new password."
    )
  })

  it("does not sign out a newly active account when password change resolves late", async () => {
    let finishChange: (() => void) | undefined
    state.api.changePassword.mockReturnValue(
      new Promise((resolve) => {
        finishChange = () => resolve({ message: "updated" })
      })
    )
    render(<AccountSettingsPage />)
    fireEvent.change(screen.getByLabelText("Current password"), {
      target: { value: "old-password" },
    })
    fireEvent.change(screen.getByLabelText("New password"), {
      target: { value: "new-password" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Change password" }))
    await waitFor(() => expect(api.changePassword).toHaveBeenCalled())

    setAuth({
      user: {
        user_id: "student-2",
        email: "grace@example.com",
        name: "Grace",
        role: "student",
      },
      accessToken: "access-2",
      refreshToken: "refresh-2",
    })
    finishChange?.()

    await waitFor(() =>
      expect(screen.getByLabelText("Display name")).toHaveValue("Grace")
    )
    expect(getAuth()).toMatchObject({
      user: { user_id: "student-2" },
      accessToken: "access-2",
      refreshToken: "refresh-2",
    })
    expect(state.navigate).not.toHaveBeenCalled()
  })

  it("does not apply a delayed profile response to a newly active account", async () => {
    let finishUpdate: ((user: typeof session.user) => void) | undefined
    state.api.updateProfile.mockReturnValue(
      new Promise((resolve) => {
        finishUpdate = resolve
      })
    )
    render(<AccountSettingsPage />)
    fireEvent.change(screen.getByLabelText("Display name"), {
      target: { value: "Changed on old account" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save profile" }))
    await waitFor(() => expect(api.updateProfile).toHaveBeenCalled())

    setAuth({
      user: {
        user_id: "student-2",
        email: "grace@example.com",
        name: "Grace",
        role: "student",
      },
      accessToken: "access-2",
      refreshToken: "refresh-2",
    })
    finishUpdate?.({ ...session.user, name: "Wrong account name" })

    await waitFor(() =>
      expect(screen.getByLabelText("Display name")).toHaveValue("Grace")
    )
    expect(getAuth()).toMatchObject({
      user: { user_id: "student-2", name: "Grace" },
      accessToken: "access-2",
      refreshToken: "refresh-2",
    })
  })

  it("shows a generic recovery response and submits a token reset", async () => {
    const { unmount } = render(<LoginPage />)
    fireEvent.click(screen.getByRole("button", { name: "Forgot password?" }))
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "ada@example.com" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Send reset link" }))
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "reset email is unavailable"
      )
    )
    expect(screen.getByRole("status")).not.toHaveTextContent("generic response")
    expect(api.forgotPassword).toHaveBeenCalledWith("ada@example.com")
    unmount()

    state.search = { token: "reset-token" }
    render(<LoginPage />)
    fireEvent.change(screen.getByLabelText("New password"), {
      target: { value: "replacement-password" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Show password" }))
    expect(screen.getByLabelText("New password")).toHaveAttribute(
      "type",
      "text"
    )
    fireEvent.click(screen.getByRole("button", { name: "Reset password" }))
    await waitFor(() =>
      expect(api.resetPassword).toHaveBeenCalledWith({
        token: "reset-token",
        password: "replacement-password",
      })
    )
  })

  it("keeps an invitation return URL after login", async () => {
    state.search = { redirect: "/?join_class=ABC234" }
    render(<LoginPage />)
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "ada@example.com" },
    })
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "password" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }))
    await waitFor(() =>
      expect(state.navigate).toHaveBeenCalledWith({ to: "/?join_class=ABC234" })
    )
  })

  it("shows instructions and timing before beginning a server attempt", async () => {
    state.examInfo = {
      ...state.examInfo,
      attempt_status: "in_progress",
    }
    render(
      <ExamLobby
        examId="exam-1"
        userId="student-1"
        pendingSubmission={null}
        refreshResult={vi.fn(() => Promise.resolve())}
      />
    )
    expect(
      screen.getByText("Read each question carefully.")
    ).toBeInTheDocument()
    expect(screen.getByText("25 minutes")).toBeInTheDocument()
    expect(api.getExam).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "Resume assessment" }))
    await waitFor(() => expect(api.getExam).toHaveBeenCalledWith("exam-1"))
  })

  it("starts a permitted retake with the expected submitted attempt number", async () => {
    const meta = {
      ...state.examInfo,
      opens_at: "2026-01-01T00:00:00.000Z",
      closes_at: "2026-12-31T00:00:00.000Z",
      attempt_status: "submitted" as const,
      attempt_number: 1,
      max_attempts: 2,
    }
    render(
      <ResultView
        examId="exam-1"
        userId="student-1"
        result={{
          status: "committed",
          score: 2,
          total: 4,
          answers: {},
          request_id: "receipt-1",
          review: [],
        }}
        meta={meta}
      />
    )
    fireEvent.click(screen.getByRole("button", { name: "Retake assessment" }))
    expect(api.retakeExam).not.toHaveBeenCalled()
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      "Your next timer starts as soon as you confirm."
    )
    fireEvent.click(screen.getByRole("button", { name: "Start timed attempt" }))
    await waitFor(() =>
      expect(api.retakeExam).toHaveBeenCalledWith("exam-1", 1)
    )
  })
})
