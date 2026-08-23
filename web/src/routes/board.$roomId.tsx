import { Suspense, lazy, useCallback, useEffect, useRef, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  CheckIcon,
  CopyIcon,
  RefreshCwIcon,
  UsersIcon,
  WifiOffIcon,
} from "lucide-react"

import type { BoardCanvasHandle } from "@/components/board/board-canvas"
import { PeerCursors } from "@/components/board/peer-cursors"
import { AppShell } from "@/components/layout/app-shell"
import { normaliseRoomCode } from "@/lib/room-code"
import { rememberRoom } from "@/lib/recent-rooms"
import { requireAuth } from "@/lib/guard"
import { useAuth } from "@/lib/auth"
import type { BoardElement, ClientMessage } from "@/lib/types"
import { useBoardSync } from "@/hooks/use-board-sync"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

const BoardCanvas = lazy(() => import("@/components/board/board-canvas"))

export const Route = createFileRoute("/board/$roomId")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: BoardPage,
  head: () => ({ meta: [{ title: "Live board · Slate" }] }),
})

const PERMISSION_KEY = "slate.board-perms"
const CURSOR_THROTTLE_MS = 40

function loadStudentDrawPermission(roomId: string): boolean {
  try {
    return JSON.parse(window.localStorage.getItem(PERMISSION_KEY) ?? "{}")[roomId] === true
  } catch {
    return false
  }
}

function saveStudentDrawPermission(roomId: string, allowed: boolean) {
  try {
    const stored = JSON.parse(window.localStorage.getItem(PERMISSION_KEY) ?? "{}")
    stored[roomId] = allowed
    window.localStorage.setItem(PERMISSION_KEY, JSON.stringify(stored))
  } catch {
    // best effort only
  }
}

function initialsOf(name: string): string {
  const trimmed = name.trim()
  return trimmed ? trimmed.slice(0, 2).toUpperCase() : "??"
}

function BoardPage() {
  const { roomId: rawRoomId } = Route.useParams()
  const roomId = normaliseRoomCode(rawRoomId)
  const auth = useAuth()
  const [copied, setCopied] = useState(false)
  const [studentDraw, setStudentDraw] = useState(false)

  const canvasHandle = useRef<BoardCanvasHandle>({ mergeRemote: undefined })
  const senderRef = useRef<((message: ClientMessage) => void) | null>(null)

  useEffect(() => {
    setStudentDraw(loadStudentDrawPermission(roomId))
  }, [roomId])

  useEffect(() => {
    if (roomId) rememberRoom(roomId)
  }, [roomId])

  const isTeacher = auth.user?.role === "teacher"
  const canEdit = isTeacher || studentDraw

  const onRemote = useCallback((elements: BoardElement[]) => {
    canvasHandle.current.mergeRemote?.(elements)
  }, [])

  const sync = useBoardSync({
    roomId,
    name: auth.user?.name || "Guest",
    role: isTeacher ? "teacher" : "student",
    onInit: onRemote,
    onElements: onRemote,
  })
  senderRef.current = sync.send

  const lastCursorSentRef = useRef(0)
  const onCursorMove = useCallback(
    (xPercent: number, yPercent: number) => {
      const now = Date.now()
      if (now - lastCursorSentRef.current < CURSOR_THROTTLE_MS) return
      lastCursorSentRef.current = now
      sync.send({ type: "cursor", x: xPercent, y: yPercent, tool: "pointer" })
    },
    [sync.send],
  )

  function toggleStudentDraw(allowed: boolean) {
    setStudentDraw(allowed)
    saveStudentDrawPermission(roomId, allowed)
    if (allowed) toast.success("Students can draw")
    else toast.info("Students are view-only")
  }

  async function copyRoomCode() {
    try {
      await navigator.clipboard.writeText(roomId)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error("Couldn't copy the room code")
    }
  }

  if (!roomId) {
    return (
      <AppShell title="Live board">
        <p className="text-sm text-muted-foreground">That room code isn't valid.</p>
      </AppShell>
    )
  }

  return (
    <AppShell
      title={`Board · ${roomId}`}
      description={canEdit ? "You can draw" : "View-only — ask your teacher to enable drawing"}
      padded={false}
      actions={
        <>
          <p className="sr-only" role="status" aria-live="polite">
            {sync.status === "live"
              ? "Board connected"
              : sync.status === "reconnecting"
                ? "Connection lost. Reconnecting."
                : "Connecting to board."}
          </p>
          {sync.status === "live" ? (
            <Badge variant="default">
              <span className="mr-0.5 inline-block size-1.5 rounded-full bg-primary-foreground" />
              live
            </Badge>
          ) : (
            <>
              <Badge variant="secondary">
                <WifiOffIcon data-icon="inline-start" />
                {sync.status}
              </Badge>
              {sync.status === "reconnecting" ? (
                <Button variant="outline" size="sm" onClick={sync.reconnect}>
                  <RefreshCwIcon data-icon="inline-start" />
                  Retry now
                </Button>
              ) : null}
            </>
          )}
          <Tooltip>
            <TooltipTrigger
              render={
                <Button variant="outline" size="sm" onClick={() => void copyRoomCode()}>
                  {copied ? (
                    <>
                      <CheckIcon data-icon="inline-start" />
                      Copied
                    </>
                  ) : (
                    <>
                      <CopyIcon data-icon="inline-start" />
                      {roomId}
                    </>
                  )}
                </Button>
              }
            />
            <TooltipContent>Copy room code</TooltipContent>
          </Tooltip>
          <div className="flex items-center gap-1.5">
            <UsersIcon className="size-4 text-muted-foreground" />
            <div className="flex -space-x-1.5">
              {sync.peers.slice(0, 4).map((peer) => (
                <Tooltip key={peer.id}>
                  <TooltipTrigger
                    render={
                      <Avatar className="size-6 ring-2 ring-background">
                        <AvatarFallback className="text-[10px]">
                          {initialsOf(peer.name)}
                        </AvatarFallback>
                      </Avatar>
                    }
                  >
                    <span className="sr-only">{peer.name}</span>
                  </TooltipTrigger>
                  <TooltipContent>{peer.name}</TooltipContent>
                </Tooltip>
              ))}
            </div>
            {sync.peers.length > 4 ? (
              <span className="text-xs text-muted-foreground">+{sync.peers.length - 4}</span>
            ) : null}
          </div>
          {isTeacher ? (
            <label
              htmlFor="students-draw"
              className="flex items-center gap-2 text-xs text-muted-foreground"
            >
              students draw
              <Switch
                id="students-draw"
                checked={studentDraw}
                onCheckedChange={(checked) => toggleStudentDraw(checked)}
              />
            </label>
          ) : null}
        </>
      }
    >
      <Suspense
        fallback={
          <div className="flex h-full items-center justify-center">
            <Skeleton className="size-full max-h-[80vh] max-w-[90vw] rounded-xl" />
          </div>
        }
      >
        <BoardCanvas
          roomId={roomId}
          readOnly={!canEdit}
          handle={canvasHandle}
          senderRef={senderRef}
          onCursorMove={onCursorMove}
        />
      </Suspense>
      <PeerCursors peers={sync.peers} selfId={sync.selfId} />
    </AppShell>
  )
}
