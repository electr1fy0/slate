import { useMemo, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  ArrowRightIcon,
  CopyIcon,
  DownloadIcon,
  HistoryIcon,
  InfoIcon,
  PlusIcon,
  PresentationIcon,
  Trash2Icon,
} from "lucide-react"

import {
  boardHistoryCsv,
  downloadTextFile,
  forgetBoardHistory,
  mergeBoardHistory,
  useBoardHistory,
} from "@/lib/board-history"
import { normaliseRoomCode, newRoomCode } from "@/lib/room-code"
import { forgetRoom, rememberRoom, useRecentRooms } from "@/lib/recent-rooms"
import { requireAuth } from "@/lib/guard"
import { AppShell } from "@/components/layout/app-shell"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export const Route = createFileRoute("/board/")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: BoardIndexPage,
  head: () => ({ meta: [{ title: "Live board · Continuum" }] }),
})

export function exportBoardHistory(
  entries: ReturnType<typeof mergeBoardHistory>
): void {
  if (entries.length === 0) {
    toast.info("There is no board history to export")
    return
  }
  downloadTextFile(
    "slate-board-history.csv",
    boardHistoryCsv(entries),
    "text/csv;charset=utf-8"
  )
  toast.success("Board history exported")
}

function BoardIndexPage() {
  const navigate = useNavigate()
  const recentRooms = useRecentRooms()
  const history = useBoardHistory()
  const [joinCode, setJoinCode] = useState("")
  const [filter, setFilter] = useState("")
  const entries = useMemo(
    () =>
      mergeBoardHistory(recentRooms, history).filter((entry) =>
        entry.room_id.includes(filter.trim().toLowerCase())
      ),
    [filter, history, recentRooms]
  )

  function joinBoard(code: string): void {
    const clean = normaliseRoomCode(code)
    if (!clean) {
      toast.error("Enter a room code first")
      return
    }
    rememberRoom(clean)
    void navigate({ to: "/board/$roomId", params: { roomId: clean } })
  }

  async function copyRoom(roomId: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(roomId)
      toast.success("Room code copied")
    } catch {
      toast.error("Could not copy the room code")
    }
  }

  function removeRoom(roomId: string): void {
    forgetRoom(roomId)
    forgetBoardHistory(roomId)
    toast.info("Room removed from history")
  }

  function exportHistory(): void {
    exportBoardHistory(entries)
  }

  return (
    <AppShell
      title="Live board"
      description="Join a room, review local history, or export a session handoff"
    >
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-6">
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,0.55fr)]">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <PresentationIcon data-icon="inline-start" />
                Join a board
              </CardTitle>
              <CardDescription>
                Rooms are live the moment two people open them. Ask your teacher
                for the code.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="board-index-code">Room code</FieldLabel>
                  <InputGroup>
                    <InputGroupInput
                      id="board-index-code"
                      placeholder="e.g. k7m2xw9q"
                      value={joinCode}
                      onChange={(event) => setJoinCode(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") joinBoard(joinCode)
                      }}
                      autoCapitalize="none"
                      spellCheck={false}
                    />
                    <InputGroupAddon align="inline-end">
                      <InputGroupButton
                        onClick={() => joinBoard(joinCode)}
                        disabled={!normaliseRoomCode(joinCode)}
                      >
                        Join
                        <ArrowRightIcon data-icon="inline-end" />
                      </InputGroupButton>
                    </InputGroupAddon>
                  </InputGroup>
                  <FieldDescription>Letters and numbers only.</FieldDescription>
                </Field>
              </FieldGroup>
            </CardContent>
            <CardFooter>
              <Button
                variant="secondary"
                onClick={() => joinBoard(newRoomCode())}
              >
                <PlusIcon data-icon="inline-start" />
                New room
              </Button>
            </CardFooter>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Session handoff</CardTitle>
              <CardDescription>
                Download a compact room list for a lesson plan or support
                handoff.
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <div className="flex items-end justify-between gap-3">
                <div>
                  <p className="text-2xl font-semibold tracking-tight">
                    {history.length}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    rooms with local metadata
                  </p>
                </div>
                <Badge variant="secondary">visit history local</Badge>
              </div>
              <Button
                variant="outline"
                onClick={exportHistory}
                disabled={entries.length === 0}
              >
                <DownloadIcon data-icon="inline-start" />
                Export history
              </Button>
            </CardContent>
          </Card>
        </div>

        <Alert>
          <InfoIcon />
          <AlertTitle>Board content resumes across reconnects</AlertTitle>
          <AlertDescription>
            Visit time and count stay in this browser for quick access. The
            board snapshot itself is persisted by the API and restored when the
            room is reopened.
          </AlertDescription>
        </Alert>

        <Card>
          <CardHeader className="gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <HistoryIcon data-icon="inline-start" />
                Recent rooms
              </CardTitle>
              <CardDescription>
                Metadata is inferred from this browser's room visits.
              </CardDescription>
            </div>
            <div className="w-full sm:w-56">
              <Field>
                <FieldLabel htmlFor="room-history-filter" className="sr-only">
                  Filter rooms
                </FieldLabel>
                <Input
                  id="room-history-filter"
                  placeholder="Filter by room code"
                  value={filter}
                  onChange={(event) => setFilter(event.target.value)}
                />
              </Field>
            </div>
          </CardHeader>
          <CardContent>
            {entries.length === 0 ? (
              <Empty className="min-h-56 border border-dashed">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <PresentationIcon />
                  </EmptyMedia>
                  <EmptyTitle>
                    {filter ? "No matching rooms" : "No rooms yet"}
                  </EmptyTitle>
                  <EmptyDescription>
                    {filter
                      ? "Try another code or clear the filter."
                      : "Boards you visit will show up here for quick access."}
                  </EmptyDescription>
                </EmptyHeader>
                {filter ? (
                  <Button variant="outline" onClick={() => setFilter("")}>
                    Clear room filter
                  </Button>
                ) : null}
              </Empty>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Room</TableHead>
                    <TableHead>Last opened</TableHead>
                    <TableHead>Visits</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="w-28 text-right">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {entries.map((entry) => (
                    <TableRow key={entry.room_id}>
                      <TableCell className="font-mono font-medium">
                        {entry.room_id}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {entry.last_opened_at === new Date(0).toISOString()
                          ? "Before local history"
                          : new Date(entry.last_opened_at).toLocaleString()}
                      </TableCell>
                      <TableCell>{entry.visits}</TableCell>
                      <TableCell>
                        <Badge variant="secondary">history only</Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex justify-end gap-1">
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={`Copy ${entry.room_id}`}
                            onClick={() => void copyRoom(entry.room_id)}
                          >
                            <CopyIcon />
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => joinBoard(entry.room_id)}
                          >
                            Open
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={`Remove ${entry.room_id}`}
                            onClick={() => removeRoom(entry.room_id)}
                          >
                            <Trash2Icon />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
    </AppShell>
  )
}
