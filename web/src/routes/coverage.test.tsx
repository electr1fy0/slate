import React from "react"
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { toast } from "sonner"

import { clearAuth, setAuth } from "@/lib/auth"
import type {
  Exam,
  ExamMeta,
  ExamResultsResponse,
  QuizDraft,
  ResultResponse,
} from "@/lib/types"
import { ApiError } from "@/lib/api"
import { forgetRoom, rememberRoom, useRecentRooms } from "@/lib/recent-rooms"
import {
  forgetBoardHistory,
  recordBoardVisit,
  useBoardHistory,
} from "@/lib/board-history"
import { createClassroom, setClassroomActiveRoom } from "@/lib/classrooms"
import {
  createOrLoadPendingSubmission,
  findPendingSubmissionForExam,
  writeExamDraft,
} from "@/lib/pending-submission"

import { Route as RootRoute } from "./__root"
import { Route as LoginRoute, validateLoginInput } from "./login"
import { Route as HomeRoute } from "./index"
import { Route as BoardIndexRoute, exportBoardHistory } from "./board.index"
import { Route as BoardRoomRoute } from "./board.$roomId"
import {
  Route as QuizRoute,
  applyRadioChoice,
  loadDraftAnswers,
  saveFailureStatus,
  serialiseAnswerMap,
} from "./quiz.$examId"
import { Route as OpsRoute, handleSliderValue } from "./ops"
import {
  Route as TeacherRoute,
  displayDraftTitle,
  draftCustomId,
  draftDeleteLabel,
} from "./teacher"

const state = vi.hoisted(() => {
  const apiMethods = [
    "register",
    "login",
    "listExams",
    "listClassrooms",
    "createClassroom",
    "joinClassroom",
    "classroomRoster",
    "removeClassroomMember",
    "boardHistory",
    "boardExport",
    "boardRestore",
    "getExam",
    "previewExam",
    "createExam",
    "listQuizDrafts",
    "getQuizDraft",
    "createQuizDraft",
    "updateQuizDraft",
    "deleteQuizDraft",
    "publishQuizDraft",
    "duplicateQuiz",
    "duplicateQuizDraft",
    "getAttempt",
    "saveAttempt",
    "submitSavedAttempt",
    "submitAnswers",
    "myResult",
    "patchExam",
    "closeExam",
    "deleteExam",
    "examResults",
    "submissions",
    "status",
    "setChaos",
    "resetChaos",
    "promote",
    "continuumStatus",
    "continuumAction",
    "examInfo",
    "listQuestionBank",
    "createBankQuestion",
    "deleteBankQuestion",
    "classroomInvitations",
    "inviteClassroomMember",
    "deleteClassroomInvitation",
    "updateClassroom",
    "deleteClassroom",
    "leaveClassroom",
    "updateProfile",
    "changePassword",
    "forgotPassword",
    "resetPassword",
    "retakeExam",
  ] as const
  const api = Object.fromEntries(
    apiMethods.map((method) => [method, vi.fn()])
  ) as Record<string, ReturnType<typeof vi.fn>>
  return {
    api,
    navigate: vi.fn(() => Promise.resolve()),
    invalidateQueries: vi.fn(() => Promise.resolve()),
    refetchQueries: vi.fn(() => Promise.resolve()),
    queryRefetch: vi.fn(() => Promise.resolve()),
    queryStates: new Map<string, Record<string, unknown>>(),
    search: {},
    params: { examId: "exam-1", roomId: "room-1" },
    mounted: true,
    online: true,
    pathname: "/",
    syncStatus: "live",
    syncSend: vi.fn(),
    syncReconnect: vi.fn(),
    syncOptions: null as {
      enabled?: boolean
      onInit: (elements: unknown[]) => void
      onElements: (elements: unknown[]) => void
      onPermissions?: (allowed: boolean) => void
    } | null,
    externalApiOrigin: false,
    mutationPending: false,
  }
})

vi.mock("@tanstack/react-router", async () => {
  const ReactModule = await import("react")
  return {
    createFileRoute: () => (config: Record<string, unknown>) => ({
      ...config,
      useSearch: () => state.search,
      useParams: () => state.params,
    }),
    createRootRoute: (config: Record<string, unknown>) => config,
    useNavigate: () => state.navigate,
    useBlocker: () => ({ status: "idle", proceed: vi.fn(), reset: vi.fn() }),
    useRouterState: ({ select }: { select: (value: unknown) => unknown }) =>
      select({ location: { pathname: state.pathname } }),
    redirect: (value: unknown) => value,
    Link: (props: Record<string, unknown>) =>
      ReactModule.createElement(
        "a",
        { href: typeof props.to === "string" ? props.to : "#", ...props },
        props.children as React.ReactNode
      ),
    HeadContent: () => null,
    ClientOnly: ({ children }: { children: React.ReactNode }) => children,
    Scripts: () => null,
  }
})

vi.mock("@tanstack/react-query", async () => {
  const ReactModule = await import("react")
  const defaultFor = (key: string): Record<string, unknown> => {
    if (key === "exams") return { data: [], isPending: false, isError: false }
    if (key === "submissions")
      return { data: [], isPending: false, isError: false }
    if (["board-versions", "classrooms", "question-bank"].includes(key))
      return { data: [], isPending: false, isError: false }
    if (key === "quiz-drafts")
      return { data: [], isPending: false, isError: false }
    if (key === "exam-results" || key === "exam-preview")
      return { data: undefined, isPending: false, isError: false }
    return { data: undefined, isPending: false, isError: false }
  }
  const queryKey = (options: { queryKey?: readonly unknown[] }) =>
    JSON.stringify(options.queryKey ?? [])
  return {
    useQuery: (options: {
      queryKey?: readonly unknown[]
      queryFn?: () => unknown
    }) => {
      const key = options.queryKey ?? []
      const legacyKey = [key[0], ...key.slice(2)]
      const override =
        state.queryStates.get(queryKey(options)) ??
        ([
          "exams",
          "quiz-drafts",
          "exam-results",
          "exam-preview",
          "submissions",
        ].includes(String(key[0]))
          ? state.queryStates.get(JSON.stringify(legacyKey))
          : undefined) ??
        {}
      if (override.runQuery && options.queryFn) {
        void Promise.resolve(options.queryFn()).catch(() => undefined)
      }
      return {
        ...defaultFor(String(options.queryKey?.[0] ?? "")),
        ...override,
        refetch: state.queryRefetch,
        isFetching: Boolean(override.isFetching),
      }
    },
    useQueryClient: () => ({
      clear: vi.fn(),
      invalidateQueries: state.invalidateQueries,
      refetchQueries: state.refetchQueries,
      setQueryData: vi.fn(),
    }),
    useMutation: (options: {
      mutationFn?: (value?: unknown) => unknown
      onSuccess?: (value: unknown, variables?: unknown) => unknown
      onError?: (error: unknown, variables?: unknown) => unknown
    }) => {
      const invoke = async (value?: unknown) => {
        try {
          const result = await options.mutationFn?.(value)
          await options.onSuccess?.(result, value)
          return result
        } catch (error) {
          await options.onError?.(error, value)
          return undefined
        }
      }
      return {
        isPending: state.mutationPending,
        mutate: (value?: unknown) => void invoke(value),
        mutateAsync: invoke,
      }
    },
    QueryClientProvider: ({ children }: { children: React.ReactNode }) =>
      children,
    useIsFetching: () => 0,
    __react: ReactModule,
  }
})

vi.mock("@/lib/query-client", () => ({
  makeQueryClient: () => ({}),
}))

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
  },
  Toaster: () => null,
}))

vi.mock("@/lib/api", () => {
  class MockApiError extends Error {
    status: number
    code?: string

    constructor(message: string, status: number, code?: string) {
      super(message)
      this.name = "ApiError"
      this.status = status
      this.code = code
    }
  }
  return {
    api: state.api,
    ApiError: MockApiError,
    boardSocketUrl: (roomId: string, token: string | null) =>
      `ws://test/board/${roomId}${token ? `?token=${token}` : ""}`,
    hasExternalApiOrigin: () => state.externalApiOrigin,
  }
})

vi.mock("@/hooks/use-mounted", () => ({ useMounted: () => state.mounted }))
vi.mock("@/hooks/use-online-status", () => ({
  useOnlineStatus: () => state.online,
}))
vi.mock("@/hooks/use-board-sync", () => ({
  useBoardSync: (options: {
    enabled?: boolean
    onInit: (elements: unknown[]) => void
    onElements: (elements: unknown[]) => void
  }) => {
    state.syncOptions = options
    return {
      status: options.enabled === false ? "unavailable" : state.syncStatus,
      peers: [],
      selfId: "self",
      send: state.syncSend,
      reconnect: state.syncReconnect,
    }
  },
}))

vi.mock("@/components/layout/app-shell", async () => {
  const ReactModule = await import("react")
  return {
    AppShell: ({
      title,
      description,
      actions,
      children,
    }: Record<string, unknown>) =>
      ReactModule.createElement(
        "div",
        { "data-testid": "app-shell" },
        ReactModule.createElement("h1", null, title as React.ReactNode),
        description
          ? ReactModule.createElement("p", null, description as React.ReactNode)
          : null,
        actions as React.ReactNode,
        children as React.ReactNode
      ),
    ShellLink: ({ children }: { children: React.ReactNode }) =>
      ReactModule.createElement("a", null, children),
  }
})

vi.mock("@/components/classroom-dashboard", async () => {
  const ReactModule = await import("react")
  const classroom = {
    id: "class-1",
    owner_id: "teacher-1",
    name: "Systems 101",
    description: "Distributed systems",
    join_code: "ABC123",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    members: [],
    active_room_id: undefined,
  }
  return {
    classBoardRoom: (value: { active_room_id?: string; id: string }) =>
      value.active_room_id ?? `class-${value.id}`,
    ClassroomDashboard: ({
      onOpenBoard,
    }: {
      onOpenBoard?: (value: typeof classroom) => void
    }) =>
      ReactModule.createElement(
        "button",
        { onClick: () => onOpenBoard?.(classroom) },
        "Open class board"
      ),
  }
})

vi.mock("@/components/quiz-preview-dialog", async () => {
  const ReactModule = await import("react")
  return {
    QuizPreviewDialog: ({
      open,
      onOpenChange,
      title,
      loading,
      error,
      questions,
    }: Record<string, unknown>) =>
      open
        ? ReactModule.createElement(
            "div",
            { role: "dialog", "data-testid": "quiz-preview" },
            ReactModule.createElement("h2", null, title as React.ReactNode),
            loading ? "loading" : null,
            error ? String(error) : null,
            !loading && !error
              ? (questions as unknown[]).map((question) =>
                  ReactModule.createElement(
                    "p",
                    { key: JSON.stringify(question) },
                    JSON.stringify(question)
                  )
                )
              : null,
            ReactModule.createElement(
              "button",
              {
                onClick: () =>
                  (onOpenChange as (value: boolean) => void)(false),
              },
              "Close preview"
            ),
            ReactModule.createElement(
              "button",
              {
                onClick: () => (onOpenChange as (value: boolean) => void)(true),
              },
              "Keep preview open"
            )
          )
        : null,
  }
})

vi.mock("@/components/board/presence-panel", async () => {
  const ReactModule = await import("react")
  return {
    PresencePanel: ({ peers }: { peers: unknown[] }) =>
      ReactModule.createElement(
        "span",
        null,
        `Participants (${peers.length + 1})`
      ),
  }
})

vi.mock("@/components/board/board-canvas", async () => {
  const ReactModule = await import("react")
  return {
    default: (props: {
      handle: {
        current: {
          mergeRemote?: (elements: unknown[]) => void
          getSnapshot?: () => unknown[]
        }
      }
      onCursorMove: (x: number, y: number) => void
    }) => {
      props.handle.current = {
        mergeRemote: vi.fn(),
        getSnapshot: () => [{ id: "canvas-element" }],
      }
      return ReactModule.createElement(
        "div",
        null,
        ReactModule.createElement(
          "button",
          {
            "data-testid": "board-canvas",
            onClick: () => {
              props.handle.current.mergeRemote?.([{ id: "remote-element" }])
              props.onCursorMove(10, 20)
            },
          },
          "Canvas"
        ),
        ReactModule.createElement(
          "button",
          {
            "data-testid": "board-no-snapshot",
            onClick: () => {
              props.handle.current.getSnapshot = undefined
            },
          },
          "Clear snapshot"
        )
      )
    },
  }
})

function renderRoute(route: unknown) {
  const Component = (route as { component?: unknown })
    .component as React.ComponentType
  return render(<Component />)
}

function setQuery(
  key: readonly unknown[],
  value: Record<string, unknown>
): void {
  state.queryStates.set(JSON.stringify(key), value)
  if (key[0] === "exam")
    state.queryStates.set(JSON.stringify(["exam-info", ...key.slice(1)]), value)
}

const teacher = {
  user_id: "teacher-1",
  email: "teacher@example.com",
  name: "Ada Lovelace",
  role: "teacher" as const,
}

const student = {
  user_id: "student-1",
  email: "student@example.com",
  name: "Sam Learner",
  role: "student" as const,
}

const examMeta = (overrides: Partial<ExamMeta> = {}): ExamMeta => ({
  id: "exam-1",
  title: "Week 1",
  question_count: 2,
  created_at: "2026-01-01T00:00:00Z",
  opens_at: "2020-01-01T00:00:00Z",
  ...overrides,
})

const draft = (overrides: Partial<QuizDraft> = {}): QuizDraft => ({
  id: "draft-1",
  title: "Saved draft",
  questions: [
    {
      question: "Draft question",
      options: ["Yes", "No"],
      answer: 0,
      points: 2,
      feedback: "Because.",
    },
  ],
  owner_id: "teacher-1",
  status: "draft",
  question_count: 1,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
  ...overrides,
})

beforeEach(() => {
  localStorage.clear()
  Object.defineProperty(window, "confirm", {
    configurable: true,
    value: vi.fn(() => false),
  })
  clearAuth()
  vi.clearAllMocks()
  state.queryStates.clear()
  state.search = {}
  state.params = { examId: "exam-1", roomId: "room-1" }
  state.mounted = true
  state.online = true
  state.pathname = "/"
  state.syncStatus = "live"
  state.syncOptions = null
  state.externalApiOrigin = false
  state.mutationPending = false
  for (const fn of Object.values(state.api)) fn.mockReset()
})

afterEach(() => vi.restoreAllMocks())

describe("route components", () => {
  it("covers route metadata, guards, and the root document shell", () => {
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    const routeConfigs = [
      RootRoute,
      LoginRoute,
      HomeRoute,
      BoardIndexRoute,
      BoardRoomRoute,
      QuizRoute,
      OpsRoute,
      TeacherRoute,
    ]
    for (const route of routeConfigs) {
      expect(
        (route as unknown as { head: () => { meta: unknown[] } }).head().meta
          .length
      ).toBeGreaterThan(0)
    }
    for (const route of [
      BoardIndexRoute,
      BoardRoomRoute,
      QuizRoute,
      OpsRoute,
      TeacherRoute,
    ]) {
      expect(() =>
        (
          route as unknown as {
            beforeLoad: (value: { location: { href: string } }) => void
          }
        ).beforeLoad({ location: { href: "/test" } })
      ).not.toThrow()
    }

    const shell = (
      RootRoute as unknown as {
        shellComponent: React.ComponentType<{ children: React.ReactNode }>
      }
    ).shellComponent
    render(
      React.createElement(
        shell,
        null,
        React.createElement("span", null, "root child")
      )
    )
    expect(screen.getByText("root child")).toBeInTheDocument()

    const RootError = (
      RootRoute as unknown as {
        errorComponent: React.ComponentType<{ error: Error }>
      }
    ).errorComponent
    render(<RootError error={new Error("")} />)
    expect(
      screen.getByText(
        "An unexpected error occurred while rendering this page."
      )
    ).toBeInTheDocument()
  })

  it("validates login redirects and supports sign-in and registration", async () => {
    expect(
      (
        LoginRoute as unknown as {
          validateSearch: (value: Record<string, unknown>) => unknown
        }
      ).validateSearch({ redirect: "/board" })
    ).toEqual({ redirect: "/board" })
    expect(
      (
        LoginRoute as unknown as {
          validateSearch: (value: Record<string, unknown>) => unknown
        }
      ).validateSearch({ redirect: "https://evil.test" })
    ).toEqual({ redirect: undefined })
    state.search = { redirect: "/board" }
    state.api.login.mockResolvedValue({
      access_token: "a",
      refresh_token: "r",
      user: teacher,
    })
    const firstLogin = renderRoute(LoginRoute)
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "teacher@example.com" },
    })
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "password" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }))
    await waitFor(() =>
      expect(state.navigate).toHaveBeenCalledWith({ to: "/board" })
    )
    expect(screen.getByTestId("app-shell")).toHaveTextContent("Sign in")

    firstLogin.unmount()
    state.api.register.mockResolvedValue({
      access_token: "a2",
      refresh_token: "r2",
      user: student,
    })
    renderRoute(LoginRoute)
    fireEvent.click(screen.getByRole("tab", { name: "Create account" }))
    fireEvent.change(screen.getByLabelText("Display name"), {
      target: { value: "Sam" },
    })
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "student@example.com" },
    })
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "password" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Create account" }))
    await waitFor(() =>
      expect(state.api.register).toHaveBeenCalledWith({
        email: "student@example.com",
        password: "password",
        name: "Sam",
      })
    )
    fireEvent.keyDown(screen.getByLabelText("Password"), { key: "Enter" })
    await waitFor(() => expect(state.api.register).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole("tab", { name: "Sign in" }))
  })

  it("shows login validation and API error branches", async () => {
    expect(validateLoginInput("", "")).toBe(false)
    renderRoute(LoginRoute)
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled()
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "bad" },
    })
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "x" },
    })
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled()
    fireEvent.keyDown(screen.getByLabelText("Email"), { key: "Tab" })
    fireEvent.keyDown(screen.getByLabelText("Password"), { key: "Tab" })
    const disabledButton = screen.getByRole("button", { name: "Sign in" })
    disabledButton.removeAttribute("disabled")
    fireEvent.click(disabledButton)

    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "a@example.com" },
    })
    state.api.login.mockRejectedValueOnce(new ApiError("Wrong password", 401))
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }))
    await waitFor(() => expect(state.api.login).toHaveBeenCalled())
    state.api.login.mockRejectedValueOnce(new Error("network"))
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }))
    await waitFor(() => expect(state.api.login).toHaveBeenCalledTimes(2))

    state.search = { redirect: "//external.example" }
    state.api.login.mockResolvedValue({
      access_token: "a",
      refresh_token: "r",
      user: teacher,
    })
    fireEvent.keyDown(screen.getByLabelText("Email"), { key: "Enter" })
    await waitFor(() =>
      expect(state.navigate).toHaveBeenCalledWith({ to: "/" })
    )
    fireEvent.keyDown(screen.getByLabelText("Password"), { key: "Enter" })

    await waitFor(() =>
      expect(screen.getByRole("tab", { name: "Create account" })).toBeEnabled()
    )
    fireEvent.click(screen.getByRole("tab", { name: "Create account" }))
    fireEvent.change(screen.getByLabelText("Display name"), {
      target: { value: "   " },
    })
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "short" },
    })
    expect(
      screen.getByLabelText("Display name").closest("[data-invalid]")
    ).toHaveAttribute("data-invalid", "true")
  })

  it("renders home loading, errors, empty and populated catalog states", async () => {
    state.mounted = false
    setAuth({ user: student, accessToken: "a", refreshToken: "r" })
    setQuery(["exams"], { data: [], isPending: true, isError: false })
    renderRoute(HomeRoute)
    expect(screen.getByTestId("app-shell")).toHaveTextContent(
      "Protect your final submission"
    )

    cleanup()
    state.mounted = true
    setQuery(["exams"], { data: undefined, isPending: false, isError: true })
    renderRoute(HomeRoute)
    expect(
      screen.getByText("Assessment catalog unavailable")
    ).toBeInTheDocument()

    cleanup()
    setQuery(["exams"], { data: [], isPending: false, isError: false })
    renderRoute(HomeRoute)
    expect(screen.getByText("No assessments published yet")).toBeInTheDocument()

    cleanup()
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    setQuery(["exams"], {
      data: [
        examMeta({
          id: "open",
          title: "Open quiz",
          question_count: 1,
          duration_minutes: 10,
        }),
        examMeta({
          id: "future",
          title: "Future quiz",
          opens_at: "2099-01-01T00:00:00Z",
        }),
        examMeta({
          id: "closed",
          title: "Closed quiz",
          closes_at: "2020-01-01T00:00:00Z",
        }),
      ],
      isPending: false,
      isError: false,
    })
    renderRoute(HomeRoute)
    expect(screen.getByText("Deliver timed assessments")).toBeInTheDocument()
    const landingText = screen.getByTestId("app-shell").textContent
    expect(landingText).toContain(
      "same immutable answer snapshot and request ID"
    )
    expect(landingText.indexOf("Timed assessments")).toBeLessThan(
      landingText.indexOf("Classroom tools")
    )
    expect(landingText.indexOf("Open quiz")).toBeLessThan(
      landingText.indexOf("Classroom tools")
    )
    expect(screen.getAllByText("Preview").length).toBeGreaterThan(0)
    fireEvent.change(screen.getByLabelText("Room code"), {
      target: { value: "??" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Join" }))
    fireEvent.change(screen.getByLabelText("Room code"), {
      target: { value: "AbC-123" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Join" }))
    await waitFor(() =>
      expect(state.navigate).toHaveBeenCalledWith({
        to: "/board/$roomId",
        params: { roomId: "abc-123" },
      })
    )
    fireEvent.change(screen.getByLabelText("Room code"), {
      target: { value: "keyboard-room" },
    })
    fireEvent.keyDown(screen.getByLabelText("Room code"), { key: "Tab" })
    fireEvent.keyDown(screen.getByLabelText("Room code"), { key: "Enter" })
    fireEvent.click(screen.getByRole("button", { name: "New room" }))
    fireEvent.click(screen.getByRole("button", { name: "Open class board" }))
    await waitFor(() =>
      expect(state.navigate).toHaveBeenCalledWith({
        to: "/board/$roomId",
        params: { roomId: "class-class-1" },
      })
    )
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }))
    expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument()

    cleanup()
    setAuth({
      user: { ...student, name: "" },
      accessToken: "a",
      refreshToken: "r",
    })
    renderRoute(HomeRoute)
    expect(screen.getByText("??")).toBeInTheDocument()
  })

  it("handles the board index empty, filtered and history actions", async () => {
    cleanup()
    const recent = renderHook(() => useRecentRooms())
    const historySnapshot = renderHook(() => useBoardHistory())
    act(() => {
      for (const roomId of recent.result.current) forgetRoom(roomId)
      for (const entry of historySnapshot.result.current)
        forgetBoardHistory(entry.room_id)
    })
    recent.unmount()
    historySnapshot.unmount()
    for (const roomId of [
      "abc-123",
      "class-class-1",
      "room-a",
      "room-b",
      "a",
      "b",
      "c",
    ]) {
      forgetRoom(roomId)
      forgetBoardHistory(roomId)
    }
    forgetRoom("keyboard-room")
    forgetBoardHistory("keyboard-room")
    const emptyBoard = renderRoute(BoardIndexRoute)
    expect(screen.getByText("No rooms yet")).toBeInTheDocument()
    exportBoardHistory([])
    fireEvent.keyDown(screen.getByLabelText("Room code"), { key: "Tab" })
    fireEvent.keyDown(screen.getByLabelText("Room code"), { key: "Enter" })
    const emptyExport = screen.getByRole("button", { name: "Export history" })
    emptyExport.removeAttribute("disabled")
    fireEvent.click(emptyExport)

    emptyBoard.unmount()
    recordBoardVisit("real-room")
    rememberRoom("room-a")
    rememberRoom("room-b")
    renderRoute(BoardIndexRoute)
    expect(screen.getByText("room-a")).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Filter rooms"), {
      target: { value: "missing" },
    })
    expect(screen.getByText("No matching rooms")).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Filter rooms"), {
      target: { value: "room" },
    })
    const copy = screen.getAllByRole("button", { name: /Copy room-a/ })[0]
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    })
    fireEvent.click(copy)
    fireEvent.change(screen.getByLabelText("Filter rooms"), {
      target: { value: "" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Export history" }))
    const roomInput = screen.getByLabelText("Room code")
    fireEvent.change(roomInput, { target: { value: "key-room" } })
    fireEvent.keyDown(roomInput, { key: "Enter" })
    fireEvent.click(screen.getByRole("button", { name: "Join" }))
    ;(
      navigator.clipboard.writeText as ReturnType<typeof vi.fn>
    ).mockRejectedValueOnce(new Error("clipboard"))
    fireEvent.click(screen.getAllByRole("button", { name: /Copy room-b/ })[0])
    fireEvent.click(screen.getByRole("button", { name: "Remove room-a" }))
    fireEvent.click(screen.getAllByRole("button", { name: "Open" })[0])
    fireEvent.click(screen.getByRole("button", { name: "New room" }))
    await waitFor(() => expect(state.navigate).toHaveBeenCalled())
  })

  it("renders valid and invalid board rooms, transport actions, and permissions", async () => {
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    renderRoute(BoardRoomRoute)
    expect(await screen.findByTestId("board-canvas")).toBeInTheDocument()
    expect(screen.getByText("live")).toBeInTheDocument()
    state.syncOptions?.onInit([])
    state.syncOptions?.onElements([])
    fireEvent.click(screen.getByTestId("board-canvas"))
    fireEvent.click(
      screen.getByRole("button", { name: "Copy room code room-1" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Export" }))
    fireEvent.click(screen.getByRole("switch", { name: "students draw" }))
    await waitFor(() =>
      expect(state.api.boardExport).toHaveBeenCalledWith("room-1")
    )

    cleanup()
    state.params = { examId: "exam-1", roomId: "!!!" }
    renderRoute(BoardRoomRoute)
    expect(screen.getByText("That room code isn't valid.")).toBeInTheDocument()
  })

  it("explains when an external HTTP API deployment has no board realtime endpoint", async () => {
    state.externalApiOrigin = true
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })

    renderRoute(BoardRoomRoute)

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This deployment sends API requests to a separate HTTP origin and has no configured board WebSocket endpoint. Edits stay in this browser; export a snapshot before leaving to keep them."
    )
    expect(state.syncOptions?.enabled).toBe(false)
    expect(screen.getByText("unavailable")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Export" }))
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("Board snapshot exported")
    )
    expect(state.api.boardExport).not.toHaveBeenCalled()
  })

  it("covers student board permissions, reconnect controls, and best-effort failures", async () => {
    setAuth({
      user: { ...student, name: "" },
      accessToken: "a",
      refreshToken: "r",
    })
    state.syncStatus = "reconnecting"
    state.api.boardExport.mockRejectedValueOnce(
      new Error("persistence unavailable")
    )
    localStorage.setItem("slate.board-perms", "not-json")
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: vi
          .fn()
          .mockRejectedValue(new Error("clipboard unavailable")),
      },
    })
    renderRoute(BoardRoomRoute)
    expect(screen.getByText("reconnecting")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Retry now" }))
    fireEvent.click(screen.getByTestId("board-canvas"))
    fireEvent.click(screen.getByTestId("board-canvas"))
    fireEvent.click(screen.getByTestId("board-no-snapshot"))
    fireEvent.click(
      screen.getByRole("button", { name: "Copy room code room-1" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Export" }))
    await waitFor(() =>
      expect(state.api.boardExport).toHaveBeenCalledWith("room-1")
    )

    cleanup()
    state.syncStatus = "live"
    renderRoute(BoardRoomRoute)
    act(() => state.syncOptions?.onPermissions?.(true))
    await waitFor(() =>
      expect(screen.getByText("You can draw")).toBeInTheDocument()
    )

    cleanup()
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    renderRoute(BoardRoomRoute)
    fireEvent.click(screen.getByRole("switch", { name: "students draw" }))
    expect(state.syncSend).toHaveBeenCalledWith({
      type: "permissions",
      students_draw: true,
    })
    expect(
      screen.getByRole("switch", { name: "students draw" })
    ).toHaveAttribute("data-disabled")
    act(() => state.syncOptions?.onPermissions?.(true))
    expect(screen.getByRole("switch", { name: "students draw" })).toBeChecked()
  })

  it("covers quiz result review and runner gate states", async () => {
    state.params = { examId: "exam-1", roomId: "room-1" }
    const result: ResultResponse = {
      status: "committed",
      score: 1,
      total: 2,
      answers: {},
      submitted_at: "2026-01-01T00:00:00Z",
      review: [
        {
          index: 0,
          question: "One",
          options: ["A", "B"],
          chosen: null,
          correct_answer: 0,
          was_correct: false,
        },
        {
          index: 1,
          question: "Two",
          options: ["A", "B"],
          chosen: 1,
          correct_answer: 0,
          was_correct: false,
        },
        {
          index: 2,
          question: "Three",
          options: ["A", "B"],
          chosen: 0,
          correct_answer: 0,
          was_correct: true,
        },
        {
          index: 3,
          question: "Out of range",
          options: ["A", "B"],
          chosen: 9,
          correct_answer: null,
          was_correct: false,
        },
      ],
    }
    setQuery(["result", "anonymous", "exam-1"], {
      data: result,
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    expect(screen.getByText("50%")).toBeInTheDocument()
    expect(screen.getByText("skipped")).toBeInTheDocument()
    expect(screen.getAllByText("Your answer:").length).toBeGreaterThan(0)

    cleanup()
    createOrLoadPendingSubmission(
      { userId: "anonymous", examId: "exam-1", attemptNumber: 1 },
      { "0": 1 },
      () => "recovered-request"
    )
    setQuery(["result", "anonymous", "exam-1"], {
      data: { ...result, request_id: "recovered-request" },
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    expect(
      screen.getByText("Receipt recovered for this request")
    ).toBeInTheDocument()
    expect(screen.getByText("recovered-request")).toBeInTheDocument()
    await waitFor(() =>
      expect(findPendingSubmissionForExam("anonymous", "exam-1")).toBeNull()
    )

    cleanup()
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: true,
      isError: false,
    })
    renderRoute(QuizRoute)
    cleanup()
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: undefined,
      isPending: true,
      isError: false,
    })
    renderRoute(QuizRoute)

    cleanup()
    setQuery(["exam", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("Closed", 403, "EXAM_CLOSED"),
    })
    renderRoute(QuizRoute)
    expect(screen.getByText("This quiz has closed")).toBeInTheDocument()
    cleanup()
    setQuery(["exam", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("Down", 503),
    })
    renderRoute(QuizRoute)
    expect(screen.getByText("Quiz unavailable")).toBeInTheDocument()
  })

  it("runs an online quiz and submits through the confirmation dialog", async () => {
    const exam: Exam = {
      id: "exam-1",
      title: "Practice",
      question_count: 2,
      questions: [
        { index: 0, question: "One?", options: ["A", "B"] },
        { index: 1, question: "Two?", options: ["C", "D"] },
      ],
      attempt: { answers: {}, version: 1, started_at: Date.now() },
    }
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: exam,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    localStorage.setItem("slate.quiz.exam-1", "42")
    state.api.saveAttempt.mockResolvedValue({
      version: 2,
      request_id: "save",
      answers: { "0": 1 },
    })
    state.api.submitAnswers.mockResolvedValue({
      status: "committed",
      request_id: "r",
      version: 2,
      score: 2,
      total: 2,
    })
    renderRoute(QuizRoute)
    fireEvent.click(screen.getAllByRole("radio")[1])
    fireEvent.click(screen.getByRole("button", { name: "Next" }))
    fireEvent.click(screen.getAllByRole("radio")[1])
    fireEvent.click(screen.getByRole("button", { name: "Previous" }))
    fireEvent.click(screen.getByRole("button", { name: /^Go to question 2/ }))
    fireEvent.click(screen.getByRole("button", { name: "Submit quiz" }))
    expect(screen.getByText("Submit your answers?")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Save and submit" }))
    await waitFor(() =>
      expect(state.api.submitAnswers).toHaveBeenCalledWith(
        "exam-1",
        expect.objectContaining({
          request_id: expect.any(String),
          answers: expect.any(Object),
        })
      )
    )
  })

  it("reloads and retries the exact locally persisted request snapshot", async () => {
    const scope = { userId: "anonymous", examId: "exam-1", attemptNumber: 1 }
    const pending = createOrLoadPendingSubmission(
      scope,
      { "0": 1, "1": 0 },
      () => "same-after-reload"
    )
    writeExamDraft(scope, { 0: 0, 1: 1 })
    const exam: Exam = {
      id: "exam-1",
      title: "Saved snapshot",
      question_count: 2,
      questions: [
        { index: 0, question: "One?", options: ["A", "B"] },
        { index: 1, question: "Two?", options: ["C", "D"] },
      ],
      attempt: { answers: { "0": 0, "1": 1 }, version: 3, attempt_number: 1 },
    }
    state.api.submitAnswers.mockResolvedValue({
      status: "duplicate",
      request_id: pending.requestId,
      version: 4,
      score: 1,
      total: 2,
    })
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: exam,
      isPending: false,
      isError: false,
    })

    renderRoute(QuizRoute)

    await waitFor(() =>
      expect(state.api.submitAnswers).toHaveBeenCalledWith("exam-1", {
        request_id: "same-after-reload",
        answers: { "0": 1, "1": 0 },
      })
    )
    expect(screen.getAllByRole("radio")[1]).toHaveAttribute(
      "aria-checked",
      "true"
    )
    expect(findPendingSubmissionForExam("anonymous", "exam-1")).toMatchObject({
      requestId: "same-after-reload",
      answers: { "0": 1, "1": 0 },
    })
  })

  it("shows a matching saved receipt after the exam window closes", () => {
    const pending = createOrLoadPendingSubmission(
      { userId: "anonymous", examId: "exam-1", attemptNumber: 1 },
      { "0": 1 },
      () => "deadline-committed-request"
    )
    setQuery(["result", "anonymous", "exam-1"], {
      data: {
        status: "committed",
        score: 1,
        total: 1,
        answers: { "0": 1 },
        request_id: pending.requestId,
        review: [
          {
            index: 0,
            question: "One?",
            options: ["A", "B"],
            chosen: 1,
            correct_answer: 1,
            was_correct: true,
          },
        ],
      } satisfies ResultResponse,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("exam closed", 403, "EXAM_CLOSED"),
    })

    renderRoute(QuizRoute)

    expect(
      screen.getByText("Receipt recovered for this request")
    ).toBeInTheDocument()
    expect(screen.queryByText("This quiz has closed")).not.toBeInTheDocument()
  })

  it("does not reuse another authenticated user's cached exam result", () => {
    setAuth({
      user: student,
      accessToken: "student-token",
      refreshToken: "student-refresh",
    })
    setQuery(["result", "anonymous", "exam-1"], {
      data: {
        status: "committed",
        score: 1,
        total: 2,
        answers: {},
        review: [],
      } satisfies ResultResponse,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "student-1", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("missing", 404),
    })

    renderRoute(QuizRoute)

    expect(screen.getByText("Quiz not found")).toBeInTheDocument()
    expect(screen.queryByText("50%")).not.toBeInTheDocument()
  })

  it("does not reuse another authenticated user's cached exam attempt", () => {
    setAuth({
      user: student,
      accessToken: "student-token",
      refreshToken: "student-refresh",
    })
    setQuery(["result", "student-1", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "exam-1"], {
      data: {
        id: "exam-1",
        title: "Previous student's attempt",
        question_count: 1,
        questions: [
          { index: 0, question: "Private prior answer", options: ["A", "B"] },
        ],
        attempt: { answers: { "0": 1 }, version: 4, started_at: 1000 },
        deadline_ms: 60_000,
      },
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "student-1", "exam-1"], {
      data: undefined,
      isPending: true,
      isError: false,
    })

    renderRoute(QuizRoute)

    expect(
      screen.queryByText("Previous student's attempt")
    ).not.toBeInTheDocument()
    expect(screen.queryByText("Private prior answer")).not.toBeInTheDocument()
    expect(
      document.querySelectorAll('[data-slot="skeleton"]').length
    ).toBeGreaterThan(0)
  })

  it("retries a throttled submit with the original request ID and payload", async () => {
    const exam: Exam = {
      id: "exam-1",
      title: "Throttled submission",
      question_count: 1,
      questions: [{ index: 0, question: "One?", options: ["A", "B"] }],
    }
    const requestId = "00000000-0000-4000-8000-000000000001"
    vi.spyOn(crypto, "randomUUID").mockReturnValue(requestId)
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: exam,
      isPending: false,
      isError: false,
    })
    state.api.submitAnswers
      .mockRejectedValueOnce(new ApiError("throttled", 429))
      .mockImplementationOnce((_examId, payload) =>
        Promise.resolve({
          status: "duplicate",
          request_id: payload.request_id,
          version: 1,
          score: 1,
          total: 1,
        })
      )

    renderRoute(QuizRoute)
    fireEvent.click(screen.getByRole("radio", { name: "A" }))
    fireEvent.click(screen.getByRole("button", { name: "Submit quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Save and submit" }))

    await waitFor(
      () => expect(state.api.submitAnswers).toHaveBeenCalledTimes(2),
      { timeout: 3000 }
    )
    const first = state.api.submitAnswers.mock.calls[0]
    const second = state.api.submitAnswers.mock.calls[1]
    expect(second).toEqual(first)
    expect(second[1]).toMatchObject({
      request_id: requestId,
      answers: { "0": 0 },
    })
  })

  it("describes synchronous reconciliation responses as completed, not accepted", async () => {
    const status = {
      mode: "aws",
      region: "ap-southeast-2",
      active_backend: "A",
      epoch: 2,
      paused: false,
      policy: {
        version: "v1",
        confirmation_probes: 3,
        probe_interval_ms: 5000,
      },
      backends: [
        {
          id: "A",
          healthy: true,
          liveness: true,
          ready: true,
          reason: "ready",
        },
        {
          id: "B",
          healthy: true,
          liveness: true,
          ready: true,
          reason: "ready",
        },
      ],
      events: [],
      metrics: { failovers: 0, suppressed: 0, recovery_ms: null },
    }
    state.api.continuumStatus.mockResolvedValue(status)
    state.api.continuumAction.mockResolvedValue({
      scanned: 2,
      processed: 2,
      conflict_count: 0,
    })
    setQuery(["continuum-status", "admin"], {
      data: status,
      isPending: false,
      isError: false,
      runQuery: true,
    })

    renderRoute(OpsRoute)
    fireEvent.change(screen.getByLabelText("Admin token"), {
      target: { value: "admin" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: "Reconcile pending work" })
    )

    await waitFor(() =>
      expect(toast.info).toHaveBeenCalledWith(
        "Reconciliation completed; refreshing observed status"
      )
    )
    expect(
      screen.getByText(/recent 50 control-plane events/)
    ).toBeInTheDocument()
    expect(screen.getByText("Fenced writes · not reported")).toBeInTheDocument()
  })

  it("does not automatically resend a terminally blocked request after reload", async () => {
    const pending = createOrLoadPendingSubmission(
      { userId: "anonymous", examId: "exam-1", attemptNumber: 1 },
      { "0": 1 },
      () => "terminal-request"
    )
    state.api.submitAnswers.mockRejectedValueOnce(
      new ApiError("exam closed", 403, "EXAM_CLOSED")
    )
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: {
        id: "exam-1",
        title: "Closed",
        question_count: 1,
        questions: [{ index: 0, question: "One?", options: ["A", "B"] }],
      },
      isPending: false,
      isError: false,
    })

    renderRoute(QuizRoute)
    await waitFor(() =>
      expect(screen.getByText("Submission stopped")).toBeInTheDocument()
    )
    expect(findPendingSubmissionForExam("anonymous", "exam-1")).toMatchObject({
      requestId: pending.requestId,
      status: "blocked",
    })
    expect(state.api.submitAnswers).toHaveBeenCalledTimes(1)

    cleanup()
    renderRoute(QuizRoute)
    await act(async () => new Promise((resolve) => setTimeout(resolve, 50)))

    expect(state.api.submitAnswers).toHaveBeenCalledTimes(1)
    expect(findPendingSubmissionForExam("anonymous", "exam-1")).toMatchObject({
      requestId: pending.requestId,
      status: "blocked",
    })
  })

  it("checks a pending receipt again before presenting an expired exam", async () => {
    createOrLoadPendingSubmission(
      { userId: "anonymous", examId: "exam-1", attemptNumber: 1 },
      { "0": 1 },
      () => "deadline-receipt"
    )
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("temporarily unavailable", 503),
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("exam closed", 403, "EXAM_CLOSED"),
    })

    renderRoute(QuizRoute)

    await waitFor(() => expect(state.queryRefetch).toHaveBeenCalled())
    await waitFor(() =>
      expect(screen.getByText("This quiz has closed")).toBeInTheDocument()
    )
  })

  it("covers quiz error gates and result-review edge cases", () => {
    setQuery(["result", "anonymous", "exam-1"], {
      data: {
        status: "committed",
        score: 0,
        total: 0,
        answers: {},
        review: [
          {
            index: 0,
            question: "Correct",
            options: ["A", "B"],
            chosen: 0,
            correct_answer: 0,
            was_correct: true,
          },
          {
            index: 1,
            question: "Unknown",
            options: ["A", "B"],
            chosen: 0,
            correct_answer: null,
            was_correct: false,
          },
        ],
      } satisfies ResultResponse,
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    expect(screen.getAllByText("0%").length).toBeGreaterThan(0)
    expect(screen.getByText("Correct")).toBeInTheDocument()
    expect(screen.getByText("?")).toBeInTheDocument()

    for (const [error, title] of [
      [new ApiError("Not open", 403, "EXAM_NOT_OPEN"), "Not open yet"],
      [new ApiError("Missing", 404), "Quiz not found"],
      [new Error("network"), "Quiz unavailable"],
    ] as const) {
      cleanup()
      setQuery(["result", "anonymous", "exam-1"], {
        data: undefined,
        isPending: false,
        isError: false,
      })
      setQuery(["exam", "anonymous", "exam-1"], {
        data: undefined,
        isPending: false,
        isError: true,
        error,
      })
      renderRoute(QuizRoute)
      expect(screen.getByText(title)).toBeInTheDocument()
    }
  })

  it("covers runner persistence, autosave conflicts, offline submission, and timeout", async () => {
    const exam: Exam = {
      id: "exam-1",
      title: "Timed practice",
      question_count: 2,
      questions: [
        { index: 0, question: "One?", options: ["A", "B"] },
        { index: 1, question: "Two?", options: ["C", "D"] },
      ],
      attempt: { answers: {}, version: 0, started_at: Date.now() },
      deadline_ms: Date.now() + 120_000,
    }
    localStorage.setItem("slate.quiz.exam-1", "{broken")
    expect(loadDraftAnswers("exam-1")).toEqual({})
    expect(serialiseAnswerMap({ 0: 1, 1: "invalid" as never })).toEqual({
      "0": 1,
    })
    expect(saveFailureStatus(true)).toBe("error")
    expect(saveFailureStatus(false)).toBe("offline")
    const radioChoice = vi.fn()
    applyRadioChoice(1, radioChoice)
    applyRadioChoice("1", radioChoice)
    expect(radioChoice).toHaveBeenCalledWith(1)
    localStorage.setItem(
      "slate.quiz.exam-1",
      JSON.stringify({ "0": 1, ignored: "bad" })
    )
    const originalGetItem = window.localStorage.getItem.bind(
      window.localStorage
    )
    const getItem = vi
      .spyOn(window.localStorage, "getItem")
      .mockImplementation((key) => {
        if (key.endsWith(".request-id"))
          throw new Error("request id storage blocked")
        return originalGetItem(key)
      })
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: exam,
      isPending: false,
      isError: false,
    })
    state.api.saveAttempt.mockResolvedValue({
      status: "saved",
      request_id: "save-1",
      answers: { "0": 1 },
      version: 2,
    })
    state.api.getAttempt.mockResolvedValue({ answers: { "0": 0 }, version: 3 })
    renderRoute(QuizRoute)
    getItem.mockRestore()
    expect(screen.getByRole("status")).toHaveTextContent("seconds remaining")
    fireEvent.click(screen.getAllByRole("radio")[0])
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 950))
    })
    await waitFor(() => expect(state.api.saveAttempt).toHaveBeenCalled())

    state.api.saveAttempt.mockRejectedValueOnce(new ApiError("conflict", 409))
    fireEvent.click(screen.getAllByRole("radio")[1])
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 950))
    })
    await waitFor(() => expect(state.api.getAttempt).toHaveBeenCalled())

    state.api.saveAttempt.mockRejectedValueOnce(new ApiError("conflict", 409))
    state.api.getAttempt.mockRejectedValueOnce(new Error("gone"))
    fireEvent.click(screen.getAllByRole("radio")[0])
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 950))
    })
    state.api.saveAttempt.mockRejectedValueOnce(new Error("save failed"))
    fireEvent.click(screen.getAllByRole("radio")[1])
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 950))
    })
    getItem.mockRestore()

    cleanup()
    localStorage.clear()
    state.online = false
    state.api.submitAnswers.mockResolvedValue({
      status: "committed",
      request_id: "offline",
      version: 1,
      score: 1,
      total: 2,
    })
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    const offlineExam = {
      ...exam,
      question_count: 3,
      questions: [
        ...exam.questions,
        { index: 2, question: "Three?", options: ["E", "F"] },
      ],
      deadline_ms: undefined,
      attempt: undefined,
    }
    setQuery(["exam", "anonymous", "exam-1"], {
      data: offlineExam,
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    fireEvent.click(screen.getAllByRole("radio")[0])
    fireEvent.click(screen.getByRole("button", { name: /^Go to question 3/ }))
    fireEvent.click(screen.getByRole("button", { name: "Submit quiz" }))
    expect(screen.getByText(/unanswered/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Buffer submission" }))
    expect(
      screen.getByText("Submission buffered on this device")
    ).toBeInTheDocument()
    expect(state.api.submitAnswers).not.toHaveBeenCalled()
    expect(findPendingSubmissionForExam("anonymous", "exam-1")).toMatchObject({
      examId: "exam-1",
    })

    cleanup()
    localStorage.clear()
    state.online = true
    state.api.submitAnswers.mockRejectedValueOnce(
      new ApiError("Expired", 400, "ATTEMPT_EXPIRED")
    )
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: { ...exam, deadline_ms: undefined, attempt: undefined },
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    fireEvent.click(screen.getAllByRole("radio")[0])
    fireEvent.click(screen.getByRole("button", { name: "Next" }))
    fireEvent.click(screen.getByRole("button", { name: "Submit quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Save and submit" }))
    await waitFor(() =>
      expect(state.api.submitAnswers).toHaveBeenCalledWith(
        "exam-1",
        expect.any(Object)
      )
    )

    cleanup()
    localStorage.clear()
    state.api.submitAnswers.mockRejectedValueOnce(
      new ApiError("Already submitted", 409)
    )
    renderRoute(QuizRoute)
    fireEvent.click(screen.getAllByRole("radio")[0])
    fireEvent.click(screen.getByRole("button", { name: "Next" }))
    fireEvent.click(screen.getByRole("button", { name: "Submit quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Save and submit" }))
    await waitFor(() =>
      expect(screen.getByText("Submission stopped")).toBeInTheDocument()
    )
    expect(findPendingSubmissionForExam("anonymous", "exam-1")).toMatchObject({
      examId: "exam-1",
    })

    cleanup()
    localStorage.clear()
    state.api.submitAnswers.mockRejectedValueOnce(
      new TypeError("offline server")
    )
    renderRoute(QuizRoute)
    fireEvent.click(screen.getAllByRole("radio")[0])
    fireEvent.click(screen.getByRole("button", { name: "Next" }))
    fireEvent.click(screen.getByRole("button", { name: "Submit quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Save and submit" }))
    await waitFor(() =>
      expect(state.api.submitAnswers).toHaveBeenCalledWith(
        "exam-1",
        expect.any(Object)
      )
    )

    cleanup()
    localStorage.clear()
    state.api.submitAnswers.mockRejectedValueOnce(
      new ApiError("Server rejected", 500)
    )
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: { ...exam, deadline_ms: undefined, attempt: undefined },
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    fireEvent.click(screen.getAllByRole("radio")[0])
    fireEvent.click(screen.getByRole("button", { name: "Next" }))
    fireEvent.click(screen.getByRole("button", { name: "Submit quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Save and submit" }))
    await waitFor(() =>
      expect(state.api.submitAnswers).toHaveBeenCalledWith(
        "exam-1",
        expect.any(Object)
      )
    )

    cleanup()
    localStorage.clear()
    state.api.submitAnswers.mockResolvedValue({
      status: "committed",
      request_id: "timeout",
      version: 1,
      score: 0,
      total: 2,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: { ...exam, deadline_ms: Date.now() - 1, attempt: undefined },
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    await waitFor(() =>
      expect(state.api.submitAnswers).toHaveBeenCalledWith(
        "exam-1",
        expect.any(Object)
      )
    )

    cleanup()
    state.mutationPending = true
    setQuery(["result", "anonymous", "exam-1"], {
      data: undefined,
      isPending: false,
      isError: false,
    })
    setQuery(["exam", "anonymous", "exam-1"], {
      data: { ...exam, deadline_ms: undefined, attempt: undefined },
      isPending: false,
      isError: false,
    })
    renderRoute(QuizRoute)
    fireEvent.click(screen.getByRole("button", { name: /^Go to question 2/ }))
    expect(
      screen.getAllByRole("button", { name: /Submit/ }).at(-1)
    ).toHaveTextContent("Submitting…")
    state.mutationPending = false
  }, 15_000)

  it("covers the ops token, loading/error, and active control surfaces", async () => {
    const setSlider = vi.fn()
    handleSliderValue(setSlider, 7)([12])
    handleSliderValue(setSlider, 7)(3)
    handleSliderValue(setSlider, 7)([])
    handleSliderValue(setSlider, 7)(undefined as never)
    expect(setSlider).toHaveBeenLastCalledWith(7)

    renderRoute(OpsRoute)
    expect(screen.getByLabelText("Admin token")).toBeInTheDocument()
    setQuery(["continuum-status", "admin"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("down", 500),
      runQuery: true,
    })
    state.api.continuumStatus.mockResolvedValue({})
    fireEvent.change(screen.getByLabelText("Admin token"), {
      target: { value: "admin" },
    })
    await waitFor(() =>
      expect(screen.getByText("Control status unavailable")).toBeInTheDocument()
    )
    fireEvent.click(screen.getByRole("button", { name: "Retry status" }))

    cleanup()
    state.mounted = true
    setQuery(["continuum-status", "admin"], {
      data: undefined,
      isPending: true,
      isError: false,
    })
    renderRoute(OpsRoute)
    expect(document.querySelectorAll('[data-slot="skeleton"]').length).toBe(3)

    const status = {
      mode: "local",
      region: "ap-southeast-2",
      active_backend: "A",
      epoch: 2,
      paused: false,
      policy: {
        version: "v1",
        confirmation_probes: 3,
        probe_interval_ms: 5000,
      },
      backends: [
        {
          id: "A",
          healthy: true,
          liveness: true,
          ready: true,
          reason: "exam write probe passed",
        },
        {
          id: "B",
          healthy: true,
          liveness: true,
          ready: true,
          reason: "standby ready",
        },
      ],
      events: [],
      metrics: { failovers: 0, suppressed: 0, recovery_ms: null },
    }
    cleanup()
    state.api.continuumStatus.mockResolvedValue(status)
    state.api.continuumAction.mockResolvedValue(status)
    setQuery(["continuum-status", "admin"], {
      data: status,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    renderRoute(OpsRoute)
    expect(screen.getByText("Sydney")).toBeInTheDocument()
    expect(screen.getByText("ACTIVE")).toBeInTheDocument()
    expect(screen.getByText("epoch 2")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Partial failure · A" }))
    await waitFor(() =>
      expect(state.api.continuumAction).toHaveBeenCalledWith(
        "admin",
        "partial",
        "A"
      )
    )
  })

  it("renders the root document and root error/not-found components", () => {
    expect(
      (RootRoute as unknown as { head: () => { meta: unknown[] } }).head().meta
        .length
    ).toBeGreaterThan(0)
    const NotFound = (
      RootRoute as unknown as { notFoundComponent: React.ComponentType }
    ).notFoundComponent
    render(<NotFound />)
    expect(screen.getByText("404")).toBeInTheDocument()
    const RootError = (
      RootRoute as unknown as {
        errorComponent: React.ComponentType<{ error: Error }>
      }
    ).errorComponent
    render(<RootError error={new Error("boom")} />)
    expect(screen.getByText("Something broke")).toBeInTheDocument()
    expect(screen.getByText("boom")).toBeInTheDocument()
    const reload = vi
      .spyOn(window.location, "reload")
      .mockImplementation(() => {})
    fireEvent.click(screen.getByRole("button", { name: "Reload" }))
    expect(reload).toHaveBeenCalledOnce()
    reload.mockRestore()

    const shell = (
      RootRoute as unknown as {
        shellComponent: React.ComponentType<{ children: React.ReactNode }>
      }
    ).shellComponent
    vi.stubEnv("DEV", false)
    render(
      React.createElement(
        shell,
        null,
        React.createElement("span", null, "production shell")
      )
    )
    expect(screen.getByText("production shell")).toBeInTheDocument()
    vi.unstubAllEnvs()
  })

  it("exercises teacher tabs, quiz builder, analytics, and submissions", async () => {
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    const history = renderHook(() => useBoardHistory())
    act(() => {
      for (const entry of history.result.current)
        forgetBoardHistory(entry.room_id)
    })
    history.unmount()
    state.api.createQuizDraft.mockResolvedValue(
      draft({ id: "draft-created", title: "Quiz title" })
    )
    state.api.publishQuizDraft.mockResolvedValue(
      examMeta({ id: "published-1", title: "Quiz title", question_count: 1 })
    )
    setQuery(["exams"], {
      data: [examMeta()],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "exam-1"], {
      data: {
        exam_id: "exam-1",
        count: 1,
        average_pct: 80,
        submissions: [
          {
            student_id: "student-1",
            student_name: "",
            answers: {},
            score: 0,
            total: 0,
          },
          {
            student_id: "student-2",
            student_name: "Learner",
            answers: {},
            score: 1,
            total: 2,
          },
        ],
        question_stats: [
          {
            index: 0,
            question: "No answers",
            answered: 0,
            correct: 0,
            correct_pct: null,
            option_counts: [],
          },
          {
            index: 1,
            question: "Core concept",
            answered: 2,
            correct: 1,
            correct_pct: 25,
            option_counts: [1, 1],
          },
          {
            index: 2,
            question: "Balanced",
            answered: 2,
            correct: 1,
            correct_pct: 50,
            option_counts: [1, 1],
          },
        ],
      } satisfies ExamResultsResponse,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["submissions", "admin"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Quiz title" },
    })
    fireEvent.change(screen.getByLabelText("Prompt"), {
      target: { value: "What?" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option A"), {
      target: { value: "Yes" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option B"), {
      target: { value: "No" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() =>
      expect(state.api.publishQuizDraft).toHaveBeenCalledWith("draft-created")
    )

    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    expect(screen.getByText("Your quizzes")).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole("tab", { name: "Analytics" })[0])
    expect(screen.getByText("Class performance snapshot")).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole("tab", { name: "Submissions" })[0])
    expect(screen.getAllByText("Submissions").length).toBeGreaterThan(0)
    fireEvent.change(screen.getByLabelText("Admin token"), {
      target: { value: "admin" },
    })
    await waitFor(() =>
      expect(screen.getByText("No submissions yet")).toBeInTheDocument()
    )
  })

  it("covers quiz-builder editing, local/server drafts, and publish fallbacks", async () => {
    expect(displayDraftTitle("")).toBe("Untitled draft")
    expect(displayDraftTitle("Named draft")).toBe("Named draft")
    expect(draftCustomId(undefined)).toBe("")
    expect(draftCustomId("named-id")).toBe("named-id")
    expect(draftDeleteLabel("")).toBe("draft")
    expect(draftDeleteLabel("Named draft")).toBe("Named draft")
    vi.mocked(window.confirm).mockReturnValue(true)
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts", "teacher-1"], {
      data: [draft(), draft({ id: "server-save", title: "Server save" })],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.createQuizDraft.mockResolvedValue(draft({ id: "draft-created" }))
    state.api.updateQuizDraft.mockResolvedValue(draft({ id: "draft-1" }))
    state.api.publishQuizDraft.mockResolvedValue(
      examMeta({
        id: "published-builder",
        title: "Builder quiz",
        question_count: 1,
      })
    )

    renderRoute(TeacherRoute)
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Starter quiz" },
    })
    fireEvent.change(screen.getByLabelText("Prompt"), {
      target: { value: "Starter prompt" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option A"), {
      target: { value: "A" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option B"), {
      target: { value: "B" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: /^Save question \d+ to bank$/ })
    )
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())
    fireEvent.click(
      screen
        .getByText("Server save")
        .parentElement!.parentElement!.querySelector("button")!
    )
    state.api.updateQuizDraft.mockRejectedValueOnce(
      new Error("save update failed")
    )
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(state.api.updateQuizDraft).toHaveBeenCalled())
    state.api.updateQuizDraft.mockRejectedValueOnce(
      new ApiError("missing", 404)
    )
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())
    const localDraftLoad = screen
      .getAllByRole("button", { name: /^Load (local|workspace) draft / })
      .find((button) => {
        let node: HTMLElement | null = button
        while (node) {
          if (node.textContent.includes("Starter quiz")) return true
          node = node.parentElement
        }
        return false
      })
    expect(localDraftLoad).toBeDefined()
    fireEvent.click(localDraftLoad!)
    fireEvent.click(
      screen
        .getByText("Saved draft")
        .parentElement!.parentElement!.querySelector("button")!
    )
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "   " },
    })
    expect(
      screen.getByLabelText("Title").closest("[data-invalid]")
    ).toHaveAttribute("data-invalid", "true")
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Builder quiz" },
    })
    fireEvent.change(screen.getByLabelText("Custom ID (optional)"), {
      target: { value: "builder-id" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: /^Save question \d+ to bank$/ })
    )
    fireEvent.change(screen.getByLabelText("Prompt"), {
      target: { value: "Choose one" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option A"), {
      target: { value: "First" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option B"), {
      target: { value: "Second" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: /^Save question \d+ to bank$/ })
    )
    expect(screen.getAllByText("Choose one").length).toBeGreaterThan(0)
    fireEvent.click(
      screen.getAllByRole("button", { name: /^Use bank question / }).at(-1)!
    )
    fireEvent.click(
      screen.getByRole("button", { name: "Delete bank question Choose one" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Delete question" }))
    fireEvent.click(screen.getByRole("button", { name: "Remove question 2" }))
    fireEvent.click(screen.getByRole("button", { name: "Remove question" }))

    fireEvent.click(
      screen.getAllByRole("button", { name: /^Add option to question / })[0]
    )
    fireEvent.change(screen.getByPlaceholderText("Option C"), {
      target: { value: "Third" },
    })
    fireEvent.click(
      screen.getByRole("button", {
        name: "Mark question 1 option C as correct",
      })
    )
    fireEvent.click(
      screen.getByRole("button", { name: "Remove question 1 option C" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Remove option" }))
    fireEvent.change(screen.getByLabelText("Points"), {
      target: { value: "3" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Add question" }))
    expect(screen.getByText("Question 2")).toBeInTheDocument()
    fireEvent.change(screen.getAllByLabelText("Prompt")[0], {
      target: { value: "Updated first" },
    })
    fireEvent.click(
      screen.getAllByRole("button", { name: /^Add option to question / })[0]
    )
    fireEvent.click(
      screen.getByRole("button", { name: "Remove question 1 option C" })
    )
    fireEvent.click(screen.getByRole("button", { name: "Remove option" }))
    fireEvent.change(screen.getAllByLabelText("Points")[0], {
      target: { value: "1" },
    })
    fireEvent.change(screen.getAllByLabelText("Feedback (optional)")[0], {
      target: { value: "  " },
    })
    fireEvent.click(
      screen.getAllByRole("button", { name: /^Save question \d+ to bank$/ })[0]
    )
    fireEvent.click(screen.getByRole("button", { name: "Remove question 2" }))
    fireEvent.click(screen.getByRole("button", { name: "Remove question" }))
    for (let index = 0; index < 4; index += 1) {
      fireEvent.click(
        screen.getByRole("button", { name: /^Add option to question / })
      )
    }
    for (const [letter, value] of [
      ["C", "Third"],
      ["D", "Fourth"],
      ["E", "Fifth"],
      ["F", "Sixth"],
    ]) {
      fireEvent.change(screen.getByPlaceholderText(`Option ${letter}`), {
        target: { value },
      })
    }

    fireEvent.click(screen.getByRole("button", { name: "Preview" }))
    expect(screen.getByTestId("quiz-preview")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Close preview" }))
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() =>
      expect(state.api.updateQuizDraft).toHaveBeenCalledWith(
        "draft-1",
        expect.any(Object)
      )
    )
    state.api.updateQuizDraft.mockRejectedValueOnce(
      new ApiError("missing", 404)
    )
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(state.api.updateQuizDraft).toHaveBeenCalled())

    fireEvent.click(
      screen
        .getByText("Builder quiz")
        .parentElement!.parentElement!.querySelector("button")!
    )
    fireEvent.click(screen.getByRole("button", { name: "Delete Builder quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Delete draft" }))
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() => expect(state.api.publishQuizDraft).toHaveBeenCalled())

    cleanup()
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.createQuizDraft.mockResolvedValue(
      draft({ id: "fallback-created" })
    )
    state.api.updateQuizDraft.mockRejectedValueOnce(
      new ApiError("missing", 404)
    )
    state.api.publishQuizDraft.mockResolvedValue(
      examMeta({ id: "fallback-published", title: "Fallback" })
    )
    renderRoute(TeacherRoute)
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Fallback" },
    })
    fireEvent.change(screen.getByLabelText("Prompt"), {
      target: { value: "Prompt" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option A"), {
      target: { value: "A" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option B"), {
      target: { value: "B" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())

    state.api.updateQuizDraft.mockRejectedValueOnce(
      new ApiError("missing", 404)
    )
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() =>
      expect(state.api.publishQuizDraft).toHaveBeenCalledWith(
        "fallback-created"
      )
    )

    cleanup()
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.createQuizDraft.mockRejectedValueOnce(
      new Error("draft server offline")
    )
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    state.api.createQuizDraft.mockRejectedValueOnce(
      new ApiError("offline auth", 401)
    )
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Local only" },
    })
    fireEvent.change(screen.getByLabelText("Prompt"), {
      target: { value: "Local prompt" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option A"), {
      target: { value: "A" },
    })
    fireEvent.change(screen.getByPlaceholderText("Option B"), {
      target: { value: "B" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())
  }, 15_000)

  it("covers teacher manager actions, preview states, analytics, and submissions", async () => {
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    const classroom = createClassroom({
      name: "Teacher class",
      owner: { id: teacher.user_id, name: teacher.name, email: teacher.email },
    })
    setClassroomActiveRoom(classroom.id, "teacher-live")
    recordBoardVisit("teacher-live")
    recordBoardVisit("teacher-live")
    setClassroomActiveRoom(classroom.id, "teacher-single")
    recordBoardVisit("teacher-single")
    const exams = [
      examMeta({
        id: "open",
        title: "Open quiz",
        question_count: 1,
        duration_minutes: 10,
      }),
      examMeta({
        id: "future",
        title: "Future quiz",
        opens_at: "2099-01-01T00:00:00Z",
      }),
      examMeta({
        id: "closed",
        title: "Closed quiz",
        closes_at: "2020-01-01T00:00:00Z",
      }),
    ]
    setQuery(["exams"], {
      data: exams,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-preview", "open"], {
      data: {
        id: "open",
        title: "Open quiz",
        questions: [{ index: 0, question: "Q", options: ["A", "B"] }],
      },
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-preview", null], {
      data: undefined,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", null], {
      data: undefined,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "open"], {
      data: {
        exam_id: "open",
        count: 2,
        average_pct: 75,
        submissions: [
          {
            student_id: "s1",
            student_name: "",
            answers: {},
            score: 0,
            total: 0,
          },
          {
            student_id: "s2",
            student_name: "Learner",
            answers: {},
            score: 3,
            total: 4,
            submitted_at: "2026-01-01T12:00:00Z",
          },
        ],
        question_stats: [
          {
            index: 0,
            question: "Easy",
            answered: 0,
            correct: 0,
            correct_pct: null,
            option_counts: [],
          },
          {
            index: 1,
            question: "Hard",
            answered: 4,
            correct: 1,
            correct_pct: 25,
            option_counts: [3, 1],
          },
          {
            index: 2,
            question: "Good",
            answered: 4,
            correct: 3,
            correct_pct: 75,
            option_counts: [1, 3],
          },
        ],
      } satisfies ExamResultsResponse,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "future"], {
      data: {
        exam_id: "future",
        count: 1,
        average_pct: null,
        submissions: [],
        question_stats: [],
      },
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.duplicateQuiz.mockResolvedValue(
      draft({ id: "copy-1", title: "Copy" })
    )
    state.api.closeExam.mockResolvedValue(undefined)
    state.api.deleteExam.mockResolvedValue(undefined)
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true,
      value: vi.fn(() => "blob:test"),
    })
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true,
      value: vi.fn(),
    })

    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    recordBoardVisit("teacher-single")
    expect(screen.getByText("upcoming")).toBeInTheDocument()
    expect(screen.getByText("closed")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }))
    fireEvent.click(
      screen.getByRole("button", { name: /Open quiz.*1 question/ })
    )
    expect(
      screen.getByText("2 submissions · class average 75%")
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "CSV" }))
    fireEvent.click(screen.getAllByRole("button", { name: "Preview" })[1])
    expect(screen.getByTestId("quiz-preview")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Keep preview open" }))
    fireEvent.click(screen.getByRole("button", { name: "Close preview" }))
    fireEvent.click(screen.getAllByRole("button", { name: "Close" })[0])
    fireEvent.click(screen.getByRole("button", { name: "Close quiz" }))
    await waitFor(() =>
      expect(state.api.closeExam).toHaveBeenCalledWith("open")
    )
    fireEvent.click(screen.getByRole("button", { name: "Delete Open quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Delete quiz" }))
    await waitFor(() =>
      expect(state.api.deleteExam).toHaveBeenCalledWith("open")
    )
    fireEvent.click(
      screen.getByRole("button", { name: /Future quiz.*2 questions/ })
    )
    expect(screen.getByText("1 submission")).toBeInTheDocument()
    fireEvent.click(
      screen.getByRole("button", { name: /Future quiz.*2 questions/ })
    )
    fireEvent.click(
      screen.getByRole("button", { name: /Future quiz.*2 questions/ })
    )
    fireEvent.click(screen.getByRole("button", { name: "Delete Closed quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Delete quiz" }))
    await waitFor(() =>
      expect(state.api.deleteExam).toHaveBeenCalledWith("closed")
    )
    fireEvent.click(screen.getAllByRole("button", { name: "Duplicate" })[0])
    await waitFor(() =>
      expect(state.api.duplicateQuiz).toHaveBeenCalledWith({
        source_id: "open",
      })
    )

    cleanup()
    setQuery(["exams"], {
      data: [exams[0]],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-preview", "open"], {
      data: undefined,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.duplicateQuiz.mockResolvedValueOnce(
      draft({ id: undefined as never, title: "No id copy" })
    )
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    fireEvent.click(screen.getByRole("button", { name: "Duplicate" }))
    await waitFor(() =>
      expect(screen.getByDisplayValue("No id copy")).toBeInTheDocument()
    )

    cleanup()
    setQuery(["exams"], {
      data: exams,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-preview", "open"], {
      data: undefined,
      isPending: true,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    fireEvent.click(screen.getAllByRole("button", { name: "Preview" })[1])
    expect(screen.getByTestId("quiz-preview")).toHaveTextContent("loading")

    cleanup()
    setQuery(["exams"], {
      data: exams,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-preview", "open"], {
      data: undefined,
      isPending: false,
      isError: true,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    fireEvent.click(screen.getAllByRole("button", { name: "Preview" })[1])
    expect(screen.getByTestId("quiz-preview")).toHaveTextContent("unavailable")

    cleanup()
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Analytics" })[0])
    expect(screen.getByText("Nothing to analyze yet")).toBeInTheDocument()

    cleanup()
    setQuery(["exams"], {
      data: undefined,
      isPending: false,
      isError: true,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Analytics" })[0])
    expect(screen.getByText("Analytics unavailable")).toBeInTheDocument()

    cleanup()
    setQuery(["exams"], {
      data: [exams[0]],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "open"], {
      data: undefined,
      isPending: false,
      isError: true,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Analytics" })[0])
    expect(
      screen.getByText("Quiz catalog is ready, results are not")
    ).toBeInTheDocument()

    cleanup()
    setQuery(["exams"], {
      data: [exams[1]],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "future"], {
      data: {
        exam_id: "future",
        count: 1,
        average_pct: null,
        submissions: [],
        question_stats: [],
      },
      isPending: false,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Analytics" })[0])
    expect(
      screen.getByText("Waiting for the first submission")
    ).toBeInTheDocument()
    expect(screen.getByText("No submissions yet")).toBeInTheDocument()

    cleanup()
    localStorage.setItem("slate.admin-token", "admin")
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["submissions", "admin"], {
      data: [
        {
          pk: "SUBMISSION#quiz",
          sk: "STUDENT#s1",
          answers: { "0": 1, "1": 0 },
          version: 2,
          updated_at: 0,
          updated_at_iso: "2026-01-01T12:34:56Z",
          written_by_region: "ap-south-1",
        },
      ],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Submissions" })[0])
    await waitFor(() => expect(screen.getByText("s1")).toBeInTheDocument())
    expect(
      screen.getByText(
        (value) => value.includes("1→B") && value.includes("2→A")
      )
    ).toBeInTheDocument()
  })

  it("covers teacher loading and mutation failures", async () => {
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    setQuery(["exams"], {
      data: [],
      isPending: true,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    expect(
      document.querySelectorAll('[data-slot="skeleton"]').length
    ).toBeGreaterThanOrEqual(2)
    fireEvent.click(screen.getAllByRole("tab", { name: "Analytics" })[0])
    expect(
      document.querySelectorAll('[data-slot="skeleton"]').length
    ).toBeGreaterThanOrEqual(4)

    cleanup()
    setQuery(["exams"], {
      data: undefined,
      isPending: false,
      isError: true,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    expect(screen.getByText("Couldn't load quizzes")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }))

    cleanup()
    const exam = examMeta({ id: "error-exam", title: "Error quiz" })
    setQuery(["exams"], {
      data: [exam],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-preview", "error-exam"], {
      data: undefined,
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "error-exam"], {
      data: undefined,
      isPending: true,
      isError: false,
      runQuery: true,
    })
    state.api.closeExam.mockRejectedValueOnce(new ApiError("close failed", 409))
    state.api.deleteExam.mockRejectedValueOnce(new Error("delete failed"))
    state.api.duplicateQuiz.mockRejectedValueOnce(new Error("duplicate failed"))
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    fireEvent.click(
      screen.getByRole("button", { name: /Error quiz.*2 questions/ })
    )
    expect(document.querySelector('[data-slot="skeleton"]')).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.click(screen.getByRole("button", { name: "Close quiz" }))
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("close failed")
    )
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    fireEvent.click(screen.getByRole("button", { name: "Delete Error quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Delete quiz" }))
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("delete failed")
    )
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    fireEvent.click(screen.getByRole("button", { name: "Duplicate" }))
    await waitFor(() => {
      expect(state.api.closeExam).toHaveBeenCalledWith("error-exam")
      expect(state.api.deleteExam).toHaveBeenCalledWith("error-exam")
      expect(state.api.duplicateQuiz).toHaveBeenCalledWith({
        source_id: "error-exam",
      })
    })
    state.api.closeExam.mockRejectedValueOnce(new Error("close network"))
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.click(screen.getByRole("button", { name: "Close quiz" }))
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("close network")
    )
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    state.api.deleteExam.mockRejectedValueOnce(
      new ApiError("delete denied", 403)
    )
    fireEvent.click(screen.getByRole("button", { name: "Delete Error quiz" }))
    fireEvent.click(screen.getByRole("button", { name: "Delete quiz" }))
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("delete denied")
    )
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    state.api.duplicateQuiz.mockRejectedValueOnce(
      new ApiError("duplicate denied", 409)
    )
    fireEvent.click(screen.getByRole("button", { name: "Duplicate" }))
    await waitFor(() =>
      expect(state.api.duplicateQuiz).toHaveBeenCalledTimes(2)
    )

    cleanup()
    setQuery(["exams"], {
      data: [exam],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "error-exam"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new ApiError("results failed", 503),
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    fireEvent.click(
      screen.getByRole("button", { name: /Error quiz.*2 questions/ })
    )
    expect(screen.getByText(/Couldn't load results/)).toBeInTheDocument()

    cleanup()
    setQuery(["exams"], {
      data: [exam],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["exam-results", "error-exam"], {
      data: undefined,
      isPending: false,
      isError: true,
      error: new Error("results network"),
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Manage" })[0])
    fireEvent.click(
      screen.getByRole("button", { name: /Error quiz.*2 questions/ })
    )
    expect(screen.getByText("Couldn't load results.")).toBeInTheDocument()
  })

  it("covers publish authorization, update, and local-only fallbacks", async () => {
    const fillBuilder = () => {
      fireEvent.change(screen.getByLabelText("Title"), {
        target: { value: "Error paths" },
      })
      fireEvent.change(screen.getByLabelText("Prompt"), {
        target: { value: "Prompt" },
      })
      fireEvent.change(screen.getByPlaceholderText("Option A"), {
        target: { value: "A" },
      })
      fireEvent.change(screen.getByPlaceholderText("Option B"), {
        target: { value: "B" },
      })
    }

    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts", "teacher-1"], {
      data: [draft({ id: "server-draft", title: "Server draft" })],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.updateQuizDraft.mockRejectedValueOnce(new ApiError("gone", 404))
    state.api.createQuizDraft.mockResolvedValueOnce(draft({ id: "" }))
    renderRoute(TeacherRoute)
    fireEvent.click(
      screen
        .getByText("Server draft")
        .parentElement!.parentElement!.querySelector("button")!
    )
    fillBuilder()
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())

    cleanup()
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts", "teacher-1"], {
      data: [draft({ id: "server-draft", title: "Server draft" })],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.updateQuizDraft.mockRejectedValueOnce(new Error("update failed"))
    renderRoute(TeacherRoute)
    fireEvent.click(
      screen
        .getByText("Server draft")
        .parentElement!.parentElement!.querySelector("button")!
    )
    fillBuilder()
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() => expect(state.api.updateQuizDraft).toHaveBeenCalled())

    cleanup()
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.createQuizDraft.mockRejectedValueOnce(
      new ApiError("expired", 401)
    )
    renderRoute(TeacherRoute)
    fillBuilder()
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())

    cleanup()
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.createQuizDraft.mockRejectedValueOnce(
      new ApiError("server rejected", 500)
    )
    renderRoute(TeacherRoute)
    fillBuilder()
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())

    cleanup()
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    state.api.createQuizDraft.mockRejectedValueOnce(
      new Error("server unavailable")
    )
    renderRoute(TeacherRoute)
    fillBuilder()
    fireEvent.click(screen.getByRole("button", { name: "Publish quiz" }))
    await waitFor(() => expect(state.api.createQuizDraft).toHaveBeenCalled())

    cleanup()
    state.mutationPending = true
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["quiz-drafts"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    expect(screen.getByText("Publishing…")).toBeInTheDocument()
    state.mutationPending = false
  })

  it("covers submissions loading, error, refresh, and query execution", async () => {
    setAuth({ user: teacher, accessToken: "a", refreshToken: "r" })
    localStorage.setItem("slate.admin-token", "admin")
    state.mounted = false
    setQuery(["exams"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    setQuery(["submissions", "admin"], {
      data: [],
      isPending: true,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Submissions" })[0])
    expect(document.querySelectorAll('[data-slot="skeleton"]').length).toBe(3)

    cleanup()
    state.mounted = true
    setQuery(["submissions", "admin"], {
      data: undefined,
      isPending: false,
      isError: true,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Submissions" })[0])
    expect(screen.getByText("Couldn't load submissions")).toBeInTheDocument()

    cleanup()
    setQuery(["submissions", "admin"], {
      data: [],
      isPending: false,
      isError: false,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Submissions" })[0])
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }))
    expect(state.api.submissions).toHaveBeenCalledWith("admin")

    cleanup()
    setQuery(["submissions", "admin"], {
      data: [],
      isPending: false,
      isError: false,
      isFetching: true,
      runQuery: true,
    })
    renderRoute(TeacherRoute)
    fireEvent.click(screen.getAllByRole("tab", { name: "Submissions" })[0])
    expect(screen.getByText("Syncing").closest("button")).toBeDisabled()
  })
})
