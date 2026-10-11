import { Suspense, lazy, useCallback, useEffect, useRef, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  CheckIcon,
  CopyIcon,
  DownloadIcon,
  InfoIcon,
  RefreshCwIcon,
  WifiOffIcon,
} from "lucide-react"

import type { BoardCanvasHandle } from "@/components/board/board-canvas"
import { PeerCursors } from "@/components/board/peer-cursors"
import { PresencePanel } from "@/components/board/presence-panel"
import { BoardHistoryDialog } from "@/components/board/board-history-dialog"
import { AppShell } from "@/components/layout/app-shell"
import { normaliseRoomCode } from "@/lib/room-code"
import { rememberRoom } from "@/lib/recent-rooms"
import { requireAuth } from "@/lib/guard"
import { useAuth } from "@/lib/auth"
import { api, hasExternalApiOrigin } from "@/lib/api"
import type { BoardElement, ClientMessage } from "@/lib/types"
import { useBoardSync } from "@/hooks/use-board-sync"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

const BoardCanvas = lazy(() => import("@/components/board/board-canvas"))

export const Route = createFileRoute("/board/$roomId")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: BoardPage,
  head: () => ({ meta: [{ title: "Live board · Continuum" }] }),
})

const CURSOR_THROTTLE_MS = 40

function BoardPage() {
  const { roomId: rawRoomId } = Route.useParams()
  const roomId = normaliseRoomCode(rawRoomId)
  const auth = useAuth()
  const [copied, setCopied] = useState(false)
  const [studentDraw, setStudentDraw] = useState(false)
  const [permissionPending, setPermissionPending] = useState(false)
  const [remoteSnapshot, setRemoteSnapshot] = useState<BoardElement[] | null>(null)

  const canvasHandle = useRef<BoardCanvasHandle>({ mergeRemote: undefined, getSnapshot: undefined })
  const senderRef = useRef<((message: ClientMessage) => void) | null>(null)

  useEffect(() => {
    setStudentDraw(false)
    setRemoteSnapshot(null)
  }, [roomId])

  useEffect(() => {
    if (roomId) rememberRoom(roomId)
  }, [roomId])

  const isTeacher = auth.user?.role === "teacher"
  const canEdit = isTeacher || studentDraw
  const boardRealtimeEnabled = !hasExternalApiOrigin()

  const onRemote = useCallback((elements: BoardElement[]) => {
    canvasHandle.current.mergeRemote?.(elements)
  }, [])

  const sync = useBoardSync({
    roomId,
    sessionId: auth.user?.user_id,
    name: auth.user?.name || "Guest",
    role: isTeacher ? "teacher" : "student",
    enabled: boardRealtimeEnabled,
    onInit: setRemoteSnapshot,
    onElements: onRemote,
    onRestored: setRemoteSnapshot,
    onPermissions: (allowed) => { setStudentDraw(allowed); setPermissionPending(false) },
    onError: (message) => { setPermissionPending(false); toast.error(message) },
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
    if (sync.status !== "live") return
    setPermissionPending(true)
    sync.send({ type: "permissions", students_draw: allowed })
  }

  useEffect(() => {
    if (sync.status !== "live") setPermissionPending(false)
  }, [sync.status])

  async function copyRoomCode() {
    try {
      await navigator.clipboard.writeText(roomId)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error("Couldn't copy the room code")
    }
  }

  async function exportBoard(): Promise<void> {
    const elements = canvasHandle.current.getSnapshot?.() ?? []
    let payload: {
      format: string
      version: number
      room_id: string
      exported_at: string
      elements: BoardElement[]
    } = {
      format: "slate-board",
      version: 1,
      room_id: roomId,
      exported_at: new Date().toISOString(),
      elements,
    }
    try {
      if (boardRealtimeEnabled) {
        const persisted = await api.boardExport(roomId)
        payload = { ...payload, ...persisted, exported_at: new Date().toISOString() }
      }
    } catch {
      // The live canvas remains a useful export when the persistence API is unavailable.
    }
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = `slate-board-${roomId}.json`
    anchor.click()
    URL.revokeObjectURL(url)
    toast.success("Board snapshot exported")
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
                : sync.status === "unavailable"
                  ? "Live board sync is unavailable in this deployment."
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
          <Tooltip>
            <TooltipTrigger
              render={
                <Button variant="outline" size="sm" onClick={() => void exportBoard()}>
                  <DownloadIcon data-icon="inline-start" />
                  Export
                </Button>
              }
            />
            <TooltipContent>Download a portable board snapshot</TooltipContent>
          </Tooltip>
          <BoardHistoryDialog roomId={roomId} enabled={boardRealtimeEnabled} onRestored={setRemoteSnapshot} />
          <div className="flex items-center gap-1.5">
            <PresencePanel
              peers={sync.peers}
              selfName={auth.user?.name || "You"}
              selfRole={isTeacher ? "teacher" : "student"}
            />
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
                disabled={sync.status !== "live" || permissionPending}
                onCheckedChange={(checked) => toggleStudentDraw(checked)}
              />
            </label>
          ) : null}
        </>
      }
    >
      {!boardRealtimeEnabled ? (
        <Alert className="m-4 mb-0">
          <InfoIcon />
          <AlertTitle>Live board sync is unavailable in this deployment</AlertTitle>
          <AlertDescription>
            This deployment sends API requests to a separate HTTP origin and has no configured board WebSocket endpoint. Edits stay in this browser; export a snapshot before leaving to keep them.
          </AlertDescription>
        </Alert>
      ) : null}
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
          remoteSnapshot={remoteSnapshot}
        />
      </Suspense>
      <PeerCursors peers={sync.peers} selfId={sync.selfId} />
    </AppShell>
  )
}
