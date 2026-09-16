import { useState } from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowRightIcon,
  ClipboardListIcon,
  LogInIcon,
  LogOutIcon,
  PlusIcon,
  PresentationIcon,
  SparklesIcon,
  UserIcon,
} from "lucide-react"

import { api } from "@/lib/api"
import { clearAuth, useAuth } from "@/lib/auth"
import { recordBoardVisit } from "@/lib/board-history"
import { setClassroomActiveRoom } from "@/lib/classrooms"
import { newRoomCode, normaliseRoomCode } from "@/lib/room-code"
import { examState } from "@/lib/types"
import { useMounted } from "@/hooks/use-mounted"
import { ClassroomDashboard, classBoardRoom } from "@/components/classroom-dashboard"
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
import { Skeleton } from "@/components/ui/skeleton"

export const Route = createFileRoute("/")({
  component: HomePage,
  head: () => ({ meta: [{ title: "Slate — Classroom boards & quizzes" }] }),
})

function HomePage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const mounted = useMounted()
  const auth = useAuth()
  const [joinCode, setJoinCode] = useState("")
  const isTeacher = auth.user?.role === "teacher"

  const examsQuery = useQuery({
    queryKey: ["exams"],
    queryFn: api.listExams,
    enabled: mounted,
  })

  function signOut(): void {
    clearAuth()
    void queryClient.invalidateQueries()
    toast.info("Signed out")
  }

  function joinBoard(code: string): void {
    const clean = normaliseRoomCode(code)
    if (!clean) {
      toast.error("Enter a room code first")
      return
    }
    recordBoardVisit(clean)
    void navigate({ to: "/board/$roomId", params: { roomId: clean } })
  }

  function openClassBoard(classroom: Parameters<typeof classBoardRoom>[0]): void {
    const roomId = classBoardRoom(classroom)
    setClassroomActiveRoom(classroom.id, roomId)
    recordBoardVisit(roomId)
    void navigate({ to: "/board/$roomId", params: { roomId } })
  }

  return (
    <AppShell title="Classroom dashboard" description="Your classes, live boards, and quizzes at a glance">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-6">
        <section className="flex flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-2xl font-semibold tracking-tight md:text-3xl">
              {isTeacher ? "Run today's class" : "Join today's class"}
            </h2>
            <Badge variant="secondary">{isTeacher ? "Teacher view" : "Learner view"}</Badge>
          </div>
          <p className="max-w-2xl text-sm text-muted-foreground">
            {isTeacher
              ? "Organize a roster, open a live board, publish a quiz, and keep the room visible while class is in motion."
              : "Select a class, join the live board, and keep your published quizzes close at hand."}
          </p>
        </section>

        <ClassroomDashboard onOpenBoard={openClassBoard} />

        <div className="grid gap-4 lg:grid-cols-[minmax(0,1.3fr)_minmax(18rem,0.7fr)]">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <PresentationIcon data-icon="inline-start" />
                Join a live board
              </CardTitle>
              <CardDescription>
                Rooms are live the moment two people open them. Share the code with your class.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="room-code">Room code</FieldLabel>
                  <InputGroup>
                    <InputGroupInput
                      id="room-code"
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
                      <InputGroupButton onClick={() => joinBoard(joinCode)}>
                        Join
                        <ArrowRightIcon data-icon="inline-end" />
                      </InputGroupButton>
                    </InputGroupAddon>
                  </InputGroup>
                  <FieldDescription>Letters and numbers only.</FieldDescription>
                </Field>
              </FieldGroup>
            </CardContent>
            <CardFooter className="flex-wrap gap-2">
              <Button variant="secondary" onClick={() => joinBoard(newRoomCode())}>
                <PlusIcon data-icon="inline-start" />
                New room
              </Button>
              {isTeacher ? (
                <Button nativeButton={false} render={<Link to="/teacher" />}>
                  <ClipboardListIcon data-icon="inline-start" />
                  Create a quiz
                </Button>
              ) : null}
            </CardFooter>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <UserIcon data-icon="inline-start" />
                You
              </CardTitle>
              <CardDescription>
                {auth.user ? "Signed in to this browser." : "Sign in to join boards and quizzes."}
              </CardDescription>
            </CardHeader>
            <CardContent>
              {auth.user ? (
                <div className="flex items-center gap-3">
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary text-sm font-medium text-primary-foreground">
                    {auth.user.name.trim().slice(0, 2).toUpperCase() || "??"}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium">{auth.user.name}</p>
                    <p className="truncate text-xs text-muted-foreground">{auth.user.email}</p>
                  </div>
                  <Badge variant={isTeacher ? "default" : "secondary"} className="capitalize">
                    {auth.user.role}
                  </Badge>
                </div>
              ) : (
                <FieldDescription>
                  Your identity keeps quiz submissions and board presence honest.
                </FieldDescription>
              )}
            </CardContent>
            <CardFooter>
              {auth.user ? (
                <Button variant="outline" onClick={signOut}>
                  <LogOutIcon data-icon="inline-start" />
                  Sign out
                </Button>
              ) : (
                <Button nativeButton={false} render={<Link to="/login" />}>
                  <LogInIcon data-icon="inline-start" />
                  Sign in
                </Button>
              )}
            </CardFooter>
          </Card>
        </div>

        <section className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold">Published quizzes</h3>
              <p className="text-xs text-muted-foreground">Live catalog from the exam API.</p>
            </div>
            <Badge variant="secondary">{examsQuery.data?.length ?? "…"}</Badge>
          </div>
          {!mounted || examsQuery.isPending ? (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {[0, 1, 2].map((index) => (
                <Skeleton key={index} className="h-36 rounded-xl" />
              ))}
            </div>
          ) : examsQuery.isError ? (
            <Empty className="rounded-xl border border-dashed">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <SparklesIcon />
                </EmptyMedia>
                <EmptyTitle>Quiz catalog unavailable</EmptyTitle>
                <EmptyDescription>
                  The dashboard still works with local classes and boards. Start the API to load published quizzes.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : examsQuery.data.length === 0 ? (
            <Empty className="rounded-xl border border-dashed">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <ClipboardListIcon />
                </EmptyMedia>
                <EmptyTitle>No quizzes yet</EmptyTitle>
                <EmptyDescription>Teachers can publish one from the Teacher studio.</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {examsQuery.data.map((exam) => {
                const state = examState(exam)
                const closedForStudent = state === "closed" && auth.user?.role !== "teacher"
                return (
                  <Card key={exam.id} className="gap-3 py-4">
                    <CardHeader className="px-4">
                      <CardTitle className="text-base">{exam.title}</CardTitle>
                      <CardDescription>
                        {exam.question_count} question{exam.question_count === 1 ? "" : "s"}
                        {exam.duration_minutes ? ` · ${exam.duration_minutes} min` : ""}
                      </CardDescription>
                    </CardHeader>
                    <CardContent className="px-4">
                      {state === "open" ? <Badge variant="default">open</Badge> : (
                        <Badge variant={state === "closed" ? "destructive" : "secondary"}>{state}</Badge>
                      )}
                    </CardContent>
                    <CardFooter className="px-4">
                      {closedForStudent ? (
                        <Button size="sm" variant="outline" disabled>Closed</Button>
                      ) : (
                        <Button
                          size="sm"
                          variant={!auth.user || auth.user.role === "student" ? "default" : "outline"}
                          nativeButton={false}
                          render={<Link to="/quiz/$examId" params={{ examId: exam.id }} />}
                        >
                          {!auth.user || auth.user.role === "student" ? "Take quiz" : "Preview"}
                          <ArrowRightIcon data-icon="inline-end" />
                        </Button>
                      )}
                    </CardFooter>
                  </Card>
                )
              })}
            </div>
          )}
        </section>
      </div>
    </AppShell>
  )
}
