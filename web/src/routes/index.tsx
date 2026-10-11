import { useEffect, useMemo, useState } from "react"
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
  ShieldCheckIcon,
  SparklesIcon,
  UserIcon,
} from "lucide-react"

import { api } from "@/lib/api"
import { clearAuth, useAuth } from "@/lib/auth"
import { recordBoardVisit } from "@/lib/board-history"
import { setClassroomActiveRoom, useClassrooms } from "@/lib/classrooms"
import {
  filterAndSortAssessments,
  isAssessmentWindowOpen,
} from "@/lib/assessment-history"
import type {
  AssessmentFilterStatus,
  AssessmentSort,
} from "@/lib/assessment-history"
import { newRoomCode, normaliseRoomCode } from "@/lib/room-code"
import { examState } from "@/lib/types"
import { useMounted } from "@/hooks/use-mounted"
import {
  ClassroomDashboard,
  classBoardRoom,
} from "@/components/classroom-dashboard"
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
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export const Route = createFileRoute("/")({
  validateSearch: (
    search: Record<string, unknown>
  ): { join_class?: string } => ({
    join_class:
      typeof search.join_class === "string" ? search.join_class : undefined,
  }),
  component: HomePage,
  head: () => ({
    meta: [{ title: "Continuum — Assessment continuity for timed exams" }],
  }),
})

function HomePage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const mounted = useMounted()
  const auth = useAuth()
  const { join_class: joinClassCode } = Route.useSearch()
  const [assessmentSearch, setAssessmentSearch] = useState("")
  const [statusFilter, setStatusFilter] =
    useState<AssessmentFilterStatus>("all")
  const [classroomFilter, setClassroomFilter] = useState("all")
  const [assessmentSort, setAssessmentSort] = useState<AssessmentSort>("recent")
  const [joinCode, setJoinCode] = useState(joinClassCode ?? "")
  const classrooms = useClassrooms()
  const isTeacher = auth.user?.role === "teacher"

  const examsQuery = useQuery({
    queryKey: ["exams"],
    queryFn: api.listExams,
    enabled: mounted,
  })
  const filteredExams = useMemo(
    () =>
      filterAndSortAssessments(examsQuery.data ?? [], {
        search: assessmentSearch,
        status: statusFilter,
        classroomId: classroomFilter,
        sort: assessmentSort,
      }),
    [
      assessmentSearch,
      assessmentSort,
      classroomFilter,
      examsQuery.data,
      statusFilter,
    ]
  )

  useEffect(() => {
    if (joinClassCode) setJoinCode(joinClassCode)
  }, [joinClassCode])

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

  function openClassBoard(
    classroom: Parameters<typeof classBoardRoom>[0]
  ): void {
    const roomId = classBoardRoom(classroom)
    setClassroomActiveRoom(classroom.id, roomId)
    recordBoardVisit(roomId)
    void navigate({ to: "/board/$roomId", params: { roomId } })
  }

  return (
    <AppShell
      title="Continuum"
      description="Assessment continuity for timed exams"
    >
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-6">
        <section className="flex flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-2xl font-semibold tracking-tight md:text-3xl">
              {isTeacher
                ? "Deliver timed assessments"
                : "Protect your final submission"}
            </h2>
            <Badge variant="secondary">
              {isTeacher ? "Teacher view" : "Learner view"}
            </Badge>
          </div>
          <p className="max-w-2xl text-sm text-muted-foreground">
            {isTeacher
              ? "Give every timed exam a clear deadline and a receipt-backed completion path. Continuum checks whether exam reads and writes are safe, beyond simple process liveness."
              : auth.user
                ? "Your final answer snapshot and request ID are saved locally before submission. If a response is lost, a retry can recover the matching server receipt; the exam deadline remains authoritative."
                : "Sign in before an exam so your final answer snapshot and recovered receipt stay tied to your learner identity."}
          </p>
        </section>

        <div className="flex items-start gap-3 rounded-xl border border-primary/15 bg-primary/[0.03] p-4">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <ShieldCheckIcon className="size-5" />
          </span>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-sm font-semibold">
                Application failure recovery for final submissions
              </h3>
              <Badge variant="secondary">Continuum</Badge>
            </div>
            <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
              The app retries the same immutable answer snapshot and request ID.
              It marks a submission complete only after receiving the matching
              server receipt.
            </p>
          </div>
        </div>

        <section
          id="assessments"
          className="flex flex-col gap-3"
          aria-labelledby="assessments-heading"
        >
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 id="assessments-heading" className="text-sm font-semibold">
                Timed assessments
              </h3>
              <p className="text-xs text-muted-foreground">
                Published exams, deadlines, and confirmed submission receipts.
              </p>
            </div>
            <div className="flex items-center gap-2">
              <Badge variant="secondary">
                {examsQuery.data ? filteredExams.length : "…"}
              </Badge>
              {isTeacher ? (
                <Button
                  size="sm"
                  variant="outline"
                  nativeButton={false}
                  render={<Link to="/teacher" />}
                >
                  <ClipboardListIcon data-icon="inline-start" />
                  Manage assessments
                </Button>
              ) : null}
            </div>
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
                <EmptyTitle>Assessment catalog unavailable</EmptyTitle>
                <EmptyDescription>
                  The catalog request failed. Classroom tools remain available;
                  try again when the API responds.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : examsQuery.data.length === 0 ? (
            <Empty className="rounded-xl border border-dashed">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <ClipboardListIcon />
                </EmptyMedia>
                <EmptyTitle>No assessments published yet</EmptyTitle>
                <EmptyDescription>
                  Teachers can prepare and publish an assessment from the
                  assessment manager.
                </EmptyDescription>
              </EmptyHeader>
              {isTeacher ? (
                <Button nativeButton={false} render={<Link to="/teacher" />}>
                  Manage assessments
                  <ArrowRightIcon data-icon="inline-end" />
                </Button>
              ) : null}
            </Empty>
          ) : (
            <>
              <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
                <Input
                  aria-label="Search assessments"
                  placeholder="Search assessments"
                  value={assessmentSearch}
                  onChange={(event) => setAssessmentSearch(event.target.value)}
                />
                <Select
                  value={statusFilter}
                  onValueChange={(value) => setStatusFilter(value || "all")}
                >
                  <SelectTrigger aria-label="Filter assessments by status">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All activity</SelectItem>
                    <SelectItem value="not_started">Not started</SelectItem>
                    <SelectItem value="in_progress">In progress</SelectItem>
                    <SelectItem value="submitted">Submitted</SelectItem>
                  </SelectContent>
                </Select>
                <Select
                  value={classroomFilter}
                  onValueChange={(value) => setClassroomFilter(value || "all")}
                >
                  <SelectTrigger aria-label="Filter assessments by class">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All classes</SelectItem>
                    <SelectItem value="unassigned">No class</SelectItem>
                    {classrooms.map((classroom) => (
                      <SelectItem key={classroom.id} value={classroom.id}>
                        {classroom.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select
                  value={assessmentSort}
                  onValueChange={(value) =>
                    setAssessmentSort(value || "recent")
                  }
                >
                  <SelectTrigger aria-label="Sort assessments">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="recent">Most recent</SelectItem>
                    <SelectItem value="deadline">Deadline</SelectItem>
                    <SelectItem value="title">Title A–Z</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {filteredExams.length === 0 ? (
                <Empty className="rounded-xl border border-dashed">
                  <EmptyHeader>
                    <EmptyTitle>No matching assessments</EmptyTitle>
                    <EmptyDescription>
                      Change the search or filters to see other assessments.
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              ) : (
                <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                  {filteredExams.map((exam) => {
                    const state = examState(exam)
                    const windowOpen = isAssessmentWindowOpen(exam)
                    const attemptStatus = exam.attempt_status ?? "not_started"
                    const isStudent = auth.user?.role !== "teacher"
                    const canOpen =
                      !isStudent ||
                      attemptStatus !== "not_started" ||
                      windowOpen
                    return (
                      <Card
                        key={exam.id}
                        className="gap-3 border-primary/10 py-4"
                      >
                        <CardHeader className="px-4">
                          <CardTitle className="text-base">
                            {exam.title}
                          </CardTitle>
                          <CardDescription>
                            {exam.question_count} question
                            {exam.question_count === 1 ? "" : "s"}
                            {exam.duration_minutes
                              ? ` · ${exam.duration_minutes} min`
                              : ""}
                            {exam.classroom_id
                              ? ` · ${classrooms.find((classroom) => classroom.id === exam.classroom_id)?.name ?? "Class assessment"}`
                              : ""}
                          </CardDescription>
                        </CardHeader>
                        <CardContent className="px-4">
                          {attemptStatus === "submitted" ? (
                            <div className="flex flex-wrap items-center gap-2">
                              <Badge variant="secondary">submitted</Badge>
                              {typeof exam.score === "number" &&
                              typeof exam.total === "number" ? (
                                <span className="text-xs text-muted-foreground">
                                  Score {exam.score}/{exam.total}
                                </span>
                              ) : null}
                            </div>
                          ) : attemptStatus === "in_progress" ? (
                            <Badge variant="default">in progress</Badge>
                          ) : windowOpen ? (
                            <Badge variant="default">
                              {state === "closed"
                                ? "late work accepted"
                                : "open"}
                            </Badge>
                          ) : (
                            <Badge
                              variant={
                                state === "closed" ? "destructive" : "secondary"
                              }
                            >
                              {state}
                            </Badge>
                          )}
                        </CardContent>
                        <CardFooter className="px-4">
                          {!canOpen ? (
                            <Button size="sm" variant="outline" disabled>
                              {state === "upcoming" ? "Not open yet" : "Closed"}
                            </Button>
                          ) : (
                            <Button
                              size="sm"
                              variant={isStudent ? "default" : "outline"}
                              nativeButton={false}
                              render={
                                <Link
                                  to="/quiz/$examId"
                                  params={{ examId: exam.id }}
                                />
                              }
                            >
                              {isStudent
                                ? attemptStatus === "submitted"
                                  ? "View result"
                                  : attemptStatus === "in_progress"
                                    ? "Resume assessment"
                                    : "Start assessment"
                                : "Preview"}
                              <ArrowRightIcon data-icon="inline-end" />
                            </Button>
                          )}
                        </CardFooter>
                      </Card>
                    )
                  })}
                </div>
              )}
            </>
          )}
        </section>

        <section
          className="flex flex-col gap-4 border-t pt-5"
          aria-labelledby="classroom-tools-heading"
        >
          <div>
            <h3 id="classroom-tools-heading" className="text-sm font-semibold">
              Classroom tools
            </h3>
            <p className="text-xs text-muted-foreground">
              Roster coordination and live boards for the rest of the lesson.
            </p>
          </div>
          <ClassroomDashboard
            onOpenBoard={openClassBoard}
            initialJoinCode={joinClassCode}
          />

          <div className="grid gap-4 lg:grid-cols-[minmax(0,1.3fr)_minmax(18rem,0.7fr)]">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <PresentationIcon data-icon="inline-start" />
                  Join a live board
                </CardTitle>
                <CardDescription>
                  Rooms are live the moment two people open them. Share the code
                  with your class.
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
                    <FieldDescription>
                      Letters and numbers only.
                    </FieldDescription>
                  </Field>
                </FieldGroup>
              </CardContent>
              <CardFooter className="flex-wrap gap-2">
                <Button
                  variant="secondary"
                  onClick={() => joinBoard(newRoomCode())}
                >
                  <PlusIcon data-icon="inline-start" />
                  New room
                </Button>
                {isTeacher ? (
                  <Button nativeButton={false} render={<Link to="/teacher" />}>
                    <ClipboardListIcon data-icon="inline-start" />
                    Create an assessment
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
                  {auth.user
                    ? "Signed in to this browser."
                    : "Sign in to join boards and assessments."}
                </CardDescription>
              </CardHeader>
              <CardContent>
                {auth.user ? (
                  <div className="flex items-center gap-3">
                    <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary text-sm font-medium text-primary-foreground">
                      {auth.user.name.trim().slice(0, 2).toUpperCase() || "??"}
                    </span>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">
                        {auth.user.name}
                      </p>
                      <p className="truncate text-xs text-muted-foreground">
                        {auth.user.email}
                      </p>
                    </div>
                    <Badge
                      variant={isTeacher ? "default" : "secondary"}
                      className="capitalize"
                    >
                      {auth.user.role}
                    </Badge>
                  </div>
                ) : (
                  <FieldDescription>
                    Your identity keeps assessment submissions and board
                    presence tied to you.
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
        </section>
      </div>
    </AppShell>
  )
}
