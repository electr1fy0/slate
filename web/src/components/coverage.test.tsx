import React from "react"
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { clearAuth, setAuth } from "@/lib/auth"
import {
  getSelectedClassroomId,
  setClassroomActiveRoom,
} from "@/lib/classrooms"
import { api } from "@/lib/api"

import BoardCanvas from "./board/board-canvas"
import type { BoardCanvasHandle } from "./board/board-canvas"
import { PeerCursors } from "./board/peer-cursors"
import { PresencePanel } from "./board/presence-panel"
import {
  ClassroomDashboard,
  ClassroomSelector,
  classBoardRoom,
  classroomSelectorValue,
  handleClassroomJoinResult,
  selectClassroomFromValue,
  withSelectedClassroom,
} from "./classroom-dashboard"
import { AppShell, ShellLink } from "./layout/app-shell"
import { AppSidebar } from "./layout/app-sidebar"
import { QuickActions } from "./quick-actions"
import { QuizPreviewDialog } from "./quiz-preview-dialog"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInput,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarRail,
  SidebarSeparator,
  SidebarTrigger,
  SidebarInset,
  useSidebar,
} from "./ui/sidebar"

vi.mock("@tanstack/react-router", async () => {
  const ReactModule = await import("react")
  return {
    Link: (props: Record<string, unknown>) =>
      ReactModule.createElement(
        "a",
        { href: props.to ?? "#", ...props },
        props.children as React.ReactNode
      ),
    useNavigate: () => vi.fn(() => Promise.resolve()),
    useRouterState: ({ select }: { select: (value: unknown) => unknown }) =>
      select({ location: { pathname: "/teacher" } }),
  }
})

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn(() => Promise.resolve()) }),
}))

vi.mock("@/lib/api", () => ({
  api: {
    listClassrooms: vi.fn().mockResolvedValue([]),
    classroomRoster: vi.fn().mockResolvedValue([]),
    createClassroom: vi.fn().mockResolvedValue(undefined),
    joinClassroom: vi.fn().mockResolvedValue(undefined),
    removeClassroomMember: vi.fn().mockResolvedValue(undefined),
  },
}))

vi.mock("@excalidraw/excalidraw", async () => {
  const ReactModule = await import("react")
  return {
    CaptureUpdateAction: { NEVER: "never" },
    reconcileElements: (local: unknown[], remote: unknown[]) => [
      ...local,
      ...remote,
      { id: "deleted-merged", isDeleted: true },
    ],
    Excalidraw: (props: {
      onChange: (elements: unknown[], appState: unknown) => void
      excalidrawAPI: (api: { updateScene: (value: unknown) => void }) => void
    }) => {
      const updateScene = vi.fn()
      const version = ReactModule.useRef(2)
      ReactModule.useEffect(() => {
        props.excalidrawAPI({ updateScene })
      }, [props])
      return ReactModule.createElement(
        "div",
        null,
        ReactModule.createElement(
          "button",
          {
            "data-testid": "excalidraw",
            onClick: () => {
              const nextVersion = version.current
              version.current += 1
              props.onChange(
                [
                  { id: "local", version: nextVersion },
                  {
                    id: "deleted-local",
                    version: nextVersion,
                    isDeleted: true,
                  },
                ],
                { viewModeEnabled: false }
              )
            },
          },
          "Excalidraw"
        ),
        ReactModule.createElement(
          "button",
          {
            "data-testid": "excalidraw-stale",
            onClick: () =>
              props.onChange([{ id: "local", version: 1 }], {
                viewModeEnabled: false,
              }),
          },
          "Stale"
        )
      )
    },
  }
})

const mockedApi = vi.mocked(api, { deep: true })

function installMatchMedia(width: number) {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    writable: true,
    value: width,
  })
  const listeners = new Set<() => void>()
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query) =>
      ({
        matches: width < 768,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: (_event: string, listener: () => void) =>
          listeners.add(listener),
        removeEventListener: (_event: string, listener: () => void) =>
          listeners.delete(listener),
        dispatchEvent: vi.fn(),
      }) as unknown as MediaQueryList
  )
  return listeners
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe("board and classroom components", () => {
  beforeEach(() => {
    localStorage.clear()
    clearAuth()
    installMatchMedia(1024)
  })

  it("renders peer cursors while filtering self and incomplete positions", () => {
    const peers = [
      {
        id: "self",
        name: "Me",
        role: "teacher" as const,
        cursor: { x: 10, y: 20, tool: "pointer" },
      },
      {
        id: "peer",
        name: "Remote",
        role: "student" as const,
        cursor: { x: 15, y: 25, tool: "pointer" },
      },
      {
        id: "blank",
        name: "Blank",
        role: "student" as const,
        cursor: { x: null, y: 20, tool: "pointer" },
      },
    ]
    const { container } = render(<PeerCursors peers={peers} selfId="self" />)
    expect(container).toHaveTextContent("Remote")
    expect(container).not.toHaveTextContent("Me")
    expect(container).not.toHaveTextContent("Blank")
    const visible = container.querySelector("div[style*='left']")
    expect(visible?.getAttribute("style")).toContain("15%")
  })

  it("opens presence details for teacher and learner participants", () => {
    const { rerender } = render(
      <PresencePanel
        peers={[
          { id: "p1", name: "Student", role: "student", cursor: null },
          { id: "p2", name: "Teacher", role: "teacher", cursor: null },
        ]}
        selfName=""
        selfRole="teacher"
      />
    )
    fireEvent.click(screen.getByRole("button", { name: "Participants (3)" }))
    expect(screen.getByText("In this room")).toBeInTheDocument()
    expect(screen.getByText("You")).toBeInTheDocument()
    expect(screen.getByText("Student")).toBeInTheDocument()
    expect(screen.getByText("Teacher")).toBeInTheDocument()
    rerender(<PresencePanel peers={[]} selfName="" selfRole="student" />)
    fireEvent.click(screen.getByRole("button", { name: "Participants (1)" }))
    expect(screen.getByText("Learner")).toBeInTheDocument()
  })

  it("reconciles local and remote canvas elements, autosaves, queues changes, and reports pointers", () => {
    vi.useFakeTimers()
    const initial = [{ id: "old", version: 1, isDeleted: false }]
    localStorage.setItem(
      "slate.board.room",
      JSON.stringify([...initial, { id: "deleted", isDeleted: true }])
    )
    const handle: { current: BoardCanvasHandle } = {
      current: { mergeRemote: undefined, getSnapshot: undefined },
    }
    const sender = { current: vi.fn() }
    const onLocalEdit = vi.fn()
    const onCursorMove = vi.fn()
    const { container, unmount } = render(
      <BoardCanvas
        roomId="room"
        readOnly={false}
        handle={handle}
        senderRef={sender}
        onLocalEdit={onLocalEdit}
        onCursorMove={onCursorMove}
      />
    )
    expect(handle.current.getSnapshot?.()).toEqual([
      ...initial,
      { id: "deleted", isDeleted: true },
    ])
    handle.current.mergeRemote?.([{ id: "remote-before-change", version: 3 }])
    fireEvent.click(screen.getByTestId("excalidraw"))
    fireEvent.click(screen.getByTestId("excalidraw-stale"))
    act(() => vi.advanceTimersByTime(100))
    expect(sender.current).toHaveBeenCalledWith({
      type: "elements",
      elements: [
        { id: "local", version: 2 },
        { id: "deleted-local", version: 2, isDeleted: true },
      ],
    })
    act(() => vi.advanceTimersByTime(800))
    expect(
      JSON.parse(localStorage.getItem("slate.board.room") ?? "[]")
    ).toEqual(expect.arrayContaining([{ id: "local", version: 2 }]))
    expect(onLocalEdit).toHaveBeenCalled()

    handle.current.mergeRemote?.([
      { id: "remote" },
      { id: "gone", isDeleted: true },
    ])
    expect(handle.current.getSnapshot?.()).toEqual(
      expect.arrayContaining([{ id: "remote", version: 0 }])
    )
    const canvasContainer = container.firstElementChild as HTMLElement
    vi.spyOn(canvasContainer, "getBoundingClientRect").mockReturnValue({
      left: 0,
      top: 0,
      width: 100,
      height: 100,
      right: 100,
      bottom: 100,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    })
    fireEvent.pointerMove(canvasContainer, { clientX: 25, clientY: 75 })
    expect(onCursorMove).toHaveBeenCalledWith(25, 75)
    vi.mocked(canvasContainer.getBoundingClientRect).mockReturnValue({
      left: 0,
      top: 0,
      width: 0,
      height: 100,
      right: 0,
      bottom: 100,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    })
    fireEvent.pointerMove(canvasContainer, { clientX: 25, clientY: 75 })
    handle.current.mergeRemote?.([])
    unmount()
    expect(handle.current.mergeRemote).toBeUndefined()
    expect(handle.current.getSnapshot).toBeUndefined()
  })

  it("keeps the board usable with corrupt or unavailable storage", () => {
    vi.useFakeTimers()
    localStorage.setItem("slate.board.bad", "not json")
    const setItem = vi
      .spyOn(window.localStorage, "setItem")
      .mockImplementation(() => {
        throw new Error("blocked")
      })
    const handle: { current: BoardCanvasHandle } = {
      current: { mergeRemote: undefined, getSnapshot: undefined },
    }
    const sender = { current: null }
    const board = render(
      <BoardCanvas roomId="bad" readOnly handle={handle} senderRef={sender} />
    )
    expect(handle.current.getSnapshot?.()).toEqual([])
    fireEvent.click(screen.getByTestId("excalidraw"))
    act(() => vi.advanceTimersByTime(1000))
    expect(setItem).toHaveBeenCalled()
    board.unmount()
  })

  it("handles an empty snapshot and clears pending board timers on unmount", () => {
    vi.useFakeTimers()
    const handle: { current: BoardCanvasHandle } = {
      current: { mergeRemote: undefined, getSnapshot: undefined },
    }
    const sender = { current: vi.fn() }
    const { unmount } = render(
      <BoardCanvas
        roomId="empty"
        readOnly={false}
        handle={handle}
        senderRef={sender}
      />
    )
    expect(handle.current.getSnapshot?.()).toEqual([])
    fireEvent.click(screen.getByTestId("excalidraw"))
    fireEvent.click(screen.getByTestId("excalidraw"))
    unmount()
    act(() => vi.advanceTimersByTime(1_000))
    expect(sender.current).not.toHaveBeenCalled()
  })

  it("stops pointer reporting when the callback is removed", () => {
    const listenerSpy = vi.spyOn(HTMLElement.prototype, "addEventListener")
    const onCursorMove = vi.fn()
    const handle: { current: BoardCanvasHandle } = {
      current: { mergeRemote: undefined, getSnapshot: undefined },
    }
    const sender = { current: null }
    const { rerender, unmount } = render(
      <BoardCanvas
        roomId="pointer"
        readOnly
        handle={handle}
        senderRef={sender}
        onCursorMove={onCursorMove}
      />
    )
    const pointerHandler = listenerSpy.mock.calls.find(
      ([type]) => type === "pointermove"
    )?.[1] as ((event: PointerEvent) => void) | undefined
    expect(pointerHandler).toBeTypeOf("function")
    rerender(
      <BoardCanvas
        roomId="pointer"
        readOnly
        handle={handle}
        senderRef={sender}
      />
    )
    pointerHandler?.(
      new PointerEvent("pointermove", { clientX: 10, clientY: 10 })
    )
    expect(onCursorMove).not.toHaveBeenCalled()
    unmount()
    pointerHandler?.(
      new PointerEvent("pointermove", { clientX: 10, clientY: 10 })
    )
  })

  it("covers the classroom selector and dashboard empty state", async () => {
    expect(classroomSelectorValue(null)).toBeNull()
    expect(classroomSelectorValue({ id: "helper-class" } as never)).toBe(
      "helper-class"
    )
    selectClassroomFromValue(42)
    selectClassroomFromValue("helper-class")
    expect(withSelectedClassroom(null, () => "unused")).toBeUndefined()
    expect(
      withSelectedClassroom(
        { id: "helper-class" } as never,
        (classroom) => classroom.id
      )
    ).toBe("helper-class")
    const joinFailure = vi.fn()
    const joinSuccess = vi.fn()
    handleClassroomJoinResult(null, joinFailure, joinSuccess)
    handleClassroomJoinResult(
      { id: "helper-class" } as never,
      joinFailure,
      joinSuccess
    )
    expect(joinFailure).toHaveBeenCalledOnce()
    expect(joinSuccess).toHaveBeenCalledOnce()
    render(<ClassroomSelector onCreate={vi.fn()} />)
    expect(
      screen.getByRole("button", { name: "Set up a class" })
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Set up a class" }))

    cleanup()
    setAuth({
      user: {
        user_id: "t1",
        email: "t@example.com",
        name: "Teacher",
        role: "teacher",
      },
      accessToken: "a",
      refreshToken: "r",
    })
    const openBoard = vi.fn()
    const dashboard = render(<ClassroomDashboard onOpenBoard={openBoard} />)
    expect(screen.getByText("Create your first class")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Set up a class" }))
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.click(screen.getByRole("button", { name: "Create a class" }))
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.click(screen.getByRole("button", { name: "New class" }))
    fireEvent.click(screen.getByRole("button", { name: "Create class" }))
    mockedApi.createClassroom.mockResolvedValue({
      id: "server-class",
      owner_id: "t1",
      name: "Math",
      description: "Algebra",
      join_code: "ABC234",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      member_count: 1,
    })
    fireEvent.change(screen.getByLabelText("Class name"), {
      target: { value: "Math" },
    })
    fireEvent.change(screen.getByLabelText("Subject"), {
      target: { value: "Algebra" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Create class" }))
    await waitFor(() =>
      expect(screen.getAllByText("Math").length).toBeGreaterThan(0)
    )
    await waitFor(() => expect(mockedApi.createClassroom).toHaveBeenCalled())
    const selectedClassroomId = getSelectedClassroomId()
    expect(selectedClassroomId).toBeTruthy()
    setClassroomActiveRoom(selectedClassroomId!, "live-room")
    await waitFor(() => expect(screen.getByText("Ready")).toBeInTheDocument())
    fireEvent.click(screen.getByRole("combobox", { name: "Select a class" }))
    fireEvent.click(screen.getByRole("option", { name: "Math" }))

    fireEvent.click(screen.getByRole("button", { name: "Invite learner" }))
    fireEvent.change(screen.getByLabelText("Learner name"), {
      target: { value: "Learner" },
    })
    fireEvent.change(screen.getByLabelText("Learner email"), {
      target: { value: "learner@example.com" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    )

    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    })
    fireEvent.click(screen.getByRole("button", { name: "Copy class code" }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("ABC234"))
    fireEvent.click(screen.getByRole("button", { name: "Open live board" }))
    expect(openBoard).toHaveBeenCalled()
    expect(classBoardRoom({ id: "class-1", active_room_id: "live-room" })).toBe(
      "live-room"
    )
    expect(classBoardRoom({ id: "class/with punctuation" })).toMatch(
      /^class-ctuation-/
    )
    dashboard.unmount()

    clearAuth()
    render(<ClassroomDashboard initialJoinCode="SERVER" />)
    expect(
      screen.getByRole("link", { name: "Sign in to join this class" })
    ).toHaveAttribute("href", "/login?redirect=%2F%3Fjoin_class%3DSERVER")
    expect(
      screen.queryByRole("button", { name: "New class" })
    ).not.toBeInTheDocument()
  })
})

describe("shell and sidebar components", () => {
  it("covers the sidebar context error, desktop controls, and all wrappers", () => {
    expect(() => render(<SidebarProbeOutside />)).toThrow(
      "useSidebar must be used within"
    )
    installMatchMedia(1024)
    render(
      <SidebarProvider defaultOpen>
        <Sidebar collapsible="none">
          <SidebarHeader>Header</SidebarHeader>
          <SidebarContent>
            <SidebarGroup>
              <SidebarGroupLabel>Group</SidebarGroupLabel>
              <SidebarGroupAction aria-label="Group action" />
              <SidebarGroupContent>
                <SidebarMenu>
                  <SidebarMenuItem>
                    <SidebarMenuButton
                      tooltip="Menu item"
                      isActive
                      render={<a href="/item" />}
                    >
                      Item
                    </SidebarMenuButton>
                    <SidebarMenuAction showOnHover aria-label="Menu action" />
                    <SidebarMenuBadge>1</SidebarMenuBadge>
                  </SidebarMenuItem>
                  <SidebarMenuSub>
                    <SidebarMenuSubItem>
                      <SidebarMenuSubButton
                        render={<a href="/sub" />}
                        size="sm"
                        isActive
                      >
                        Sub
                      </SidebarMenuSubButton>
                    </SidebarMenuSubItem>
                  </SidebarMenuSub>
                  <SidebarMenuSkeleton showIcon />
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
            <SidebarSeparator />
            <SidebarInput placeholder="Search sidebar" />
          </SidebarContent>
          <SidebarFooter>Footer</SidebarFooter>
        </Sidebar>
        <SidebarInset>Inset</SidebarInset>
        <SidebarTrigger />
        <SidebarRail />
      </SidebarProvider>
    )
    expect(screen.getByText("Header")).toBeInTheDocument()
    fireEvent.click(
      screen.getAllByRole("button", { name: "Toggle Sidebar" })[0]
    )
    expect(document.cookie).toContain("sidebar_state=false")
    fireEvent.click(
      screen.getAllByRole("button", { name: "Toggle Sidebar" })[1]
    )
  })

  it("supports controlled mobile state and keyboard shortcut", () => {
    installMatchMedia(500)
    const onOpenChange = vi.fn()
    render(
      <SidebarProvider open onOpenChange={onOpenChange}>
        <Sidebar>
          <SidebarHeader>Mobile</SidebarHeader>
        </Sidebar>
        <SidebarTrigger />
      </SidebarProvider>
    )
    fireEvent.click(screen.getByRole("button", { name: "Toggle Sidebar" }))
    expect(screen.getByText("Mobile")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.keyDown(window, { key: "b", ctrlKey: true })
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByText("Mobile")).toBeInTheDocument()
  })

  it("supports controlled desktop state and forwards the shell link", () => {
    installMatchMedia(1024)
    const onOpenChange = vi.fn()
    render(
      <SidebarProvider open onOpenChange={onOpenChange}>
        <Sidebar>
          <SidebarHeader>Controlled desktop</SidebarHeader>
        </Sidebar>
        <SidebarTrigger />
      </SidebarProvider>
    )
    fireEvent.click(screen.getByRole("button", { name: "Toggle Sidebar" }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("accepts direct sidebar state updates and collapsed defaults", () => {
    installMatchMedia(1024)
    render(
      <SidebarProvider defaultOpen={false}>
        <Sidebar>
          <SidebarHeader>Collapsed desktop</SidebarHeader>
        </Sidebar>
        <SidebarSetOpenProbe />
      </SidebarProvider>
    )
    expect(
      document
        .querySelector('[data-slot="sidebar"]')
        ?.getAttribute("data-state")
    ).toBe("collapsed")
    fireEvent.click(screen.getByRole("button", { name: "Set sidebar open" }))
    expect(document.cookie).toContain("sidebar_state=true")
  })

  it("renders shell defaults and floating sidebar variants", () => {
    installMatchMedia(1024)
    render(
      <AppShell title="Compact shell" padded={false}>
        <span>Compact content</span>
      </AppShell>
    )
    expect(screen.getByText("Compact content")).toBeInTheDocument()
    cleanup()
    render(
      <SidebarProvider defaultOpen>
        <Sidebar variant="floating" collapsible="icon">
          <SidebarMenuButton
            tooltip={{ children: "Object tooltip" }}
            render={<a href="/object" />}
          >
            Object tooltip
          </SidebarMenuButton>
        </Sidebar>
        <SidebarTrigger />
      </SidebarProvider>
    )
    expect(screen.getByText("Object tooltip")).toBeInTheDocument()
    fireEvent.keyDown(window, { key: "x", ctrlKey: true })
  })

  it("renders the app shell, quick actions, and authenticated sidebar branches", async () => {
    installMatchMedia(1024)
    setAuth({
      user: {
        user_id: "t1",
        email: "teacher@example.com",
        name: "Ada Lovelace",
        role: "teacher",
      },
      accessToken: "a",
      refreshToken: "r",
    })
    render(
      <AppShell
        title="Shell title"
        description="Shell description"
        actions={
          <>
            <span>Actions</span>
            <ShellLink to="/help">Help</ShellLink>
          </>
        }
      >
        <span>Body</span>
      </AppShell>
    )
    expect(screen.getByText("Shell title")).toBeInTheDocument()
    expect(screen.getByText("Shell description")).toBeInTheDocument()
    expect(screen.getByText("Body")).toBeInTheDocument()
    expect(screen.getByText("Assessments")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Open quick actions" }))
    expect(screen.getByText("Teacher studio")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Teacher studio" }))
    fireEvent.click(screen.getByRole("button", { name: "Open quick actions" }))
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }))

    cleanup()
    clearAuth()
    render(
      <SidebarProvider>
        <AppSidebar />
      </SidebarProvider>
    )
    expect(screen.getByText("Sign in")).toBeInTheDocument()
    render(<QuickActions />)
    expect(screen.getByText("Timed assessments")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Open quick actions" }))
    // The sheet presents its create action before the board-directory link.
    // The router mock renders both as buttons.
    fireEvent.click(screen.getAllByRole("button", { name: "New board" })[0])

    cleanup()
    setAuth({
      user: {
        user_id: "s1",
        email: "student@example.com",
        name: "",
        role: "student",
      },
      accessToken: "a",
      refreshToken: "r",
    })
    render(
      <SidebarProvider>
        <AppSidebar />
      </SidebarProvider>
    )
    expect(screen.getByText("??")).toBeInTheDocument()
    expect(screen.getByText("Learner")).toBeInTheDocument()
  })
})

function SidebarProbeOutside() {
  useSidebar()
  return null
}

function SidebarSetOpenProbe() {
  const { setOpen } = useSidebar()
  return <button onClick={() => setOpen(true)}>Set sidebar open</button>
}

describe("quiz preview dialog", () => {
  it("renders loading, error, empty, and answered previews", () => {
    const onOpenChange = vi.fn()
    const { rerender } = render(
      <QuizPreviewDialog
        open
        onOpenChange={onOpenChange}
        title="Preview"
        questions={[]}
        loading
      />
    )
    expect(screen.getByLabelText("Loading quiz preview")).toBeInTheDocument()
    rerender(
      <QuizPreviewDialog
        open
        onOpenChange={onOpenChange}
        title="Preview"
        questions={[]}
        error="Unavailable"
      />
    )
    expect(screen.getByText("Unavailable")).toBeInTheDocument()
    rerender(
      <QuizPreviewDialog
        open
        onOpenChange={onOpenChange}
        title="Preview"
        questions={[]}
      />
    )
    expect(
      screen.getByText("Add at least one question to preview this quiz.")
    ).toBeInTheDocument()
    rerender(
      <QuizPreviewDialog
        open
        onOpenChange={onOpenChange}
        title=""
        questions={[]}
      />
    )
    expect(screen.getByText("Quiz preview")).toBeInTheDocument()
    rerender(
      <QuizPreviewDialog
        open
        onOpenChange={onOpenChange}
        title="Preview"
        questions={[{ question: "", options: ["", "Correct"], answer: 1 }]}
      />
    )
    expect(screen.getByText("Untitled question")).toBeInTheDocument()
    expect(screen.getByText("Empty option")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Close preview" }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
