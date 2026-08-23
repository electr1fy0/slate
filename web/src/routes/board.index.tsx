import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { ArrowRightIcon, HistoryIcon, PlusIcon, PresentationIcon, XIcon } from "lucide-react"

import { newRoomCode, normaliseRoomCode } from "@/lib/room-code"
import { forgetRoom, useRecentRooms } from "@/lib/recent-rooms"
import { requireAuth } from "@/lib/guard"
import { AppShell } from "@/components/layout/app-shell"
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
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"

export const Route = createFileRoute("/board/")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: BoardIndexPage,
  head: () => ({ meta: [{ title: "Live board · Slate" }] }),
})

function BoardIndexPage() {
  const navigate = useNavigate()
  const recentRooms = useRecentRooms()
  const [joinCode, setJoinCode] = useState("")

  function joinBoard(code: string) {
    const clean = normaliseRoomCode(code)
    if (!clean) return
    void navigate({ to: "/board/$roomId", params: { roomId: clean } })
  }

  return (
    <AppShell title="Live board" description="Join a room or start a fresh one">
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-6">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <PresentationIcon data-icon="inline-start" />
              Join a board
            </CardTitle>
            <CardDescription>
              Rooms are live the moment two people open them. Ask your teacher for the code.
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
                    <InputGroupButton onClick={() => joinBoard(joinCode)} disabled={!normaliseRoomCode(joinCode)}>
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

        <section className="flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <h2 className="flex items-center gap-1.5 text-sm font-semibold">
              <HistoryIcon className="size-4 text-muted-foreground" />
              Recent rooms
            </h2>
            {recentRooms.length > 0 ? (
              <Badge variant="secondary">{recentRooms.length}</Badge>
            ) : null}
          </div>
          {recentRooms.length === 0 ? (
            <Empty className="rounded-xl border border-dashed">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <PresentationIcon />
                </EmptyMedia>
                <EmptyTitle>No rooms yet</EmptyTitle>
                <EmptyDescription>
                  Boards you visit will show up here for quick access.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <ul className="grid gap-2 sm:grid-cols-2">
              {recentRooms.map((room) => (
                <li key={room} className="flex items-center gap-2 rounded-lg border p-2 pl-3">
                  <button
                    className="min-w-0 flex-1 rounded-md text-left text-sm font-medium hover:underline"
                    onClick={() => joinBoard(room)}
                  >
                    <span className="truncate">{room}</span>
                  </button>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Forget room ${room}`}
                    onClick={() => forgetRoom(room)}
                  >
                    <XIcon />
                  </Button>
                  <Button
                    size="sm"
                    onClick={() => joinBoard(room)}
                  >
                    Open
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </AppShell>
  )
}
