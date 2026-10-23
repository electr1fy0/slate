import { useEffect, useMemo, useRef, useState } from "react"
import { createFileRoute, Link, useBlocker } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BarChart3Icon,
  CopyIcon,
  DownloadIcon,
  EyeIcon,
  FilePenLineIcon,
  FilePlus2Icon,
  LockIcon,
  PlayIcon,
  PencilIcon,
  RadioIcon,
  RefreshCwIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { useAuth } from "@/lib/auth"
import { useAdminToken } from "@/lib/admin-token"
import { useBoardHistory } from "@/lib/board-history"
import { useSelectedClassroom } from "@/lib/classrooms"
import { requireTeacher } from "@/lib/guard"
import type {
  ExamDraft,
  ExamMeta,
  ExamResultsResponse,
  QuestionStats,
} from "@/lib/types"
import { useMounted } from "@/hooks/use-mounted"
import { AppShell } from "@/components/layout/app-shell"
import { QuizPreviewDialog } from "@/components/quiz-preview-dialog"
import { ConfirmAction } from "@/components/confirm-action"
import { EditExamDialog } from "@/components/teacher/edit-exam-dialog"
import { QuizBuilder } from "@/components/teacher/quiz-builder"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { InputGroup, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Progress } from "@/components/ui/progress"
import { Input } from "@/components/ui/input"

export const Route = createFileRoute("/teacher")({
  ssr: false,
  beforeLoad: ({ location }) => requireTeacher(location),
  component: TeacherPage,
  head: () => ({ meta: [{ title: "Assessments · Continuum" }] }),
})

function optionLetter(index: number): string {
  return String.fromCharCode(65 + index)
}

export function draftCustomId(id: string | undefined): string {
  return id ?? ""
}

export function displayDraftTitle(title: string): string {
  return title || "Untitled draft"
}

export function draftDeleteLabel(title: string): string {
  return title || "draft"
}

export function TeacherPage() {
  const auth = useAuth()
  const scopeId = auth.user?.user_id ?? "unknown"
  const [tab, setTab] = useState("create")
  const [draftSeed, setDraftSeed] = useState<ExamDraft | null>(null)
  const [builderHasChanges, setBuilderHasChanges] = useState(false)
  const builderHasChangesRef = useRef(false)
  const flushBuilderLocalRef = useRef<() => void>(() => undefined)

  builderHasChangesRef.current = builderHasChanges

  useBlocker({
    shouldBlockFn: () => {
      if (!builderHasChangesRef.current) return false
      const leave = window.confirm(
        "This quiz has changes that are not synced yet. Leave the builder? Your local autosave will remain available."
      )
      if (leave) flushBuilderLocalRef.current()
      return !leave
    },
    enableBeforeUnload: false,
  })

  function confirmBuilderChanges(): boolean {
    if (!builderHasChangesRef.current) return true
    return window.confirm(
      "This quiz has changes that are not synced yet. Leave the builder? Your local autosave will remain available."
    )
  }

  function requestTab(value: string): void {
    if (tab !== value && !confirmBuilderChanges()) {
      return
    }
    if (tab !== value) flushBuilderLocalRef.current()
    setTab(value)
  }

  return (
    <AppShell
      title="Teacher studio"
      description="Author quizzes and review submissions"
      actions={
        <Tabs value={tab} onValueChange={requestTab}>
          <TabsList>
            <TabsTrigger value="create">Create</TabsTrigger>
            <TabsTrigger value="manage">Manage</TabsTrigger>
            <TabsTrigger value="analytics">Analytics</TabsTrigger>
            <TabsTrigger value="submissions">Submissions</TabsTrigger>
          </TabsList>
        </Tabs>
      }
    >
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-6">
        <TeacherPulse />
        <Tabs value={tab}>
          <TabsContent value="create">
            <QuizBuilder
              scopeId={scopeId}
              key={`${scopeId}:${draftSeed?.id ?? "new-quiz"}`}
              initialDraft={draftSeed}
              onDirtyChange={(dirty, flushLocal) => {
                builderHasChangesRef.current = dirty
                setBuilderHasChanges(dirty)
                if (flushLocal) flushBuilderLocalRef.current = flushLocal
              }}
              onPublished={() => {
                setDraftSeed(null)
                builderHasChangesRef.current = false
                setBuilderHasChanges(false)
                setTab("manage")
              }}
            />
          </TabsContent>
          <TabsContent value="manage">
            <ExamManager
              scopeId={scopeId}
              onDuplicate={(draft) => {
                if (!confirmBuilderChanges()) return
                setDraftSeed(draft)
                setTab("create")
              }}
            />
          </TabsContent>
          <TabsContent value="analytics">
            <AnalyticsOverview scopeId={scopeId} />
          </TabsContent>
          <TabsContent value="submissions">
            <SubmissionsPanel scopeId={scopeId} />
          </TabsContent>
        </Tabs>
      </div>
    </AppShell>
  )
}

function windowBadge(meta: ExamMeta): {
  label: string
  variant: "default" | "secondary" | "destructive"
} {
  const now = Date.now()
  if (meta.opens_at && now < Date.parse(meta.opens_at)) {
    return { label: "upcoming", variant: "secondary" }
  }
  if (meta.closes_at && now > Date.parse(meta.closes_at)) {
    return { label: "closed", variant: "destructive" }
  }
  return { label: "open", variant: "default" }
}

function TeacherPulse() {
  const classroom = useSelectedClassroom()
  const history = useBoardHistory()
  const latestRoom = classroom?.active_room_id
    ? history.find((entry) => entry.room_id === classroom.active_room_id)
    : history[0]

  return (
    <div className="grid gap-3 sm:grid-cols-3">
      <Card size="sm">
        <CardHeader className="flex-row items-start justify-between space-y-0">
          <CardDescription>Current class</CardDescription>
          <UsersIcon className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent>
          <p className="truncate text-sm font-semibold">
            {classroom?.name ?? "No class selected"}
          </p>
          <p className="truncate text-xs text-muted-foreground">
            {classroom
              ? `${classroom.members.length} roster members`
              : "Create one from the dashboard"}
          </p>
        </CardContent>
      </Card>
      <Card size="sm">
        <CardHeader className="flex-row items-start justify-between space-y-0">
          <CardDescription>Live session</CardDescription>
          <RadioIcon className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent className="flex items-center justify-between gap-2">
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold">
              {classroom?.active_room_id ?? "Not started"}
            </p>
            <p className="truncate text-xs text-muted-foreground">
              {latestRoom
                ? `${latestRoom.visits} local visit${latestRoom.visits === 1 ? "" : "s"}`
                : "Board metadata is local"}
            </p>
          </div>
          <Badge variant={classroom?.active_room_id ? "default" : "secondary"}>
            {classroom?.active_room_id ? "ready" : "idle"}
          </Badge>
        </CardContent>
      </Card>
      <Card size="sm">
        <CardHeader className="flex-row items-start justify-between space-y-0">
          <CardDescription>Roster sync</CardDescription>
          <Badge variant="outline">local</Badge>
        </CardHeader>
        <CardContent>
          <p className="text-sm font-semibold">
            {classroom ? "Available" : "Waiting"}
          </p>
          <p className="truncate text-xs text-muted-foreground">
            Roster API is connected; live presence stays in the board.
          </p>
        </CardContent>
      </Card>
    </div>
  )
}

export function ExamManager({
  onDuplicate,
  scopeId,
}: {
  onDuplicate: (draft: ExamDraft) => void
  scopeId: string
}) {
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<ExamMeta | null>(null)
  const [editingExam, setEditingExam] = useState<ExamMeta | null>(null)
  const [previewExamId, setPreviewExamId] = useState<string | null>(null)
  const [search, setSearch] = useState("")
  const [stateFilter, setStateFilter] = useState("all")
  const [sortOrder, setSortOrder] = useState("newest")

  const examsQuery = useQuery({
    queryKey: ["exams", scopeId],
    queryFn: api.listExams,
  })
  const classroomsQuery = useQuery({
    queryKey: ["classrooms", scopeId],
    queryFn: api.listClassrooms,
  })

  const previewQuery = useQuery({
    queryKey: ["exam-preview", scopeId, previewExamId],
    queryFn: () => api.previewExam(previewExamId ?? ""),
    enabled: previewExamId !== null,
  })

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ["exams"] })
  }

  async function closeExam(exam: ExamMeta): Promise<void> {
    await api.closeExam(exam.id)
    toast.success(`Closed "${exam.title}"`)
    refresh()
  }

  async function deleteExam(exam: ExamMeta): Promise<void> {
    await api.deleteExam(exam.id)
    toast.success(`Deleted "${exam.title}"`)
    if (selected?.id === exam.id) setSelected(null)
    refresh()
  }

  async function duplicateExam(exam: ExamMeta): Promise<void> {
    try {
      const source = await api.duplicateQuiz({ source_id: exam.id })
      onDuplicate({
        id: source.id,
        title: source.title,
        questions: source.questions.map((question) => ({
          question: question.question,
          options: [...question.options],
          answer: question.answer,
          points: question.points,
          feedback: question.feedback,
        })),
        opens_at: source.opens_at,
        closes_at: source.closes_at,
        duration_minutes: source.duration_minutes,
        max_attempts: source.max_attempts,
        allow_late: source.allow_late,
        late_grace_minutes: source.late_grace_minutes,
        classroom_id: source.classroom_id,
        instructions: source.instructions,
      })
      toast.success(`Created a draft copy of "${exam.title}"`)
    } catch (error) {
      toast.error(
        error instanceof ApiError ? error.message : "Could not duplicate quiz"
      )
    }
  }

  const visibleExams = useMemo(() => {
    const query = search.trim().toLowerCase()
    const filtered = (examsQuery.data ?? []).filter((exam) => {
      const matchesSearch =
        !query || `${exam.title} ${exam.id}`.toLowerCase().includes(query)
      const matchesState =
        stateFilter === "all" || windowBadge(exam).label === stateFilter
      return matchesSearch && matchesState
    })
    return filtered.sort((left, right) => {
      if (sortOrder === "title") return left.title.localeCompare(right.title)
      const leftCreated = Date.parse(left.created_at) || 0
      const rightCreated = Date.parse(right.created_at) || 0
      return sortOrder === "oldest"
        ? leftCreated - rightCreated
        : rightCreated - leftCreated
    })
  }, [examsQuery.data, search, sortOrder, stateFilter])

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div className="flex flex-col gap-1">
            <CardTitle>Your quizzes</CardTitle>
            <CardDescription>
              Preview, duplicate, close, or remove a published quiz.
            </CardDescription>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void examsQuery.refetch()}
            disabled={examsQuery.isFetching}
          >
            <RefreshCwIcon data-icon="inline-start" />
            Refresh
          </Button>
        </CardHeader>
        <CardContent>
          {examsQuery.isPending ? (
            <div className="flex flex-col gap-2">
              {[0, 1].map((index) => (
                <Skeleton key={index} className="h-12 w-full" />
              ))}
            </div>
          ) : examsQuery.isError ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>Couldn't load quizzes</EmptyTitle>
                <EmptyDescription>Is the API running?</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : examsQuery.data.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>No quizzes yet</EmptyTitle>
                <EmptyDescription>
                  Publish one from the Create tab.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <>
              <div className="mb-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_10rem_10rem]">
                <label className="sr-only" htmlFor="exam-search">
                  Search quizzes
                </label>
                <Input
                  id="exam-search"
                  type="search"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search title or ID"
                  aria-label="Search quizzes"
                />
                <select
                  aria-label="Filter quizzes by status"
                  className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
                  value={stateFilter}
                  onChange={(event) => setStateFilter(event.target.value)}
                >
                  <option value="all">All statuses</option>
                  <option value="open">Open</option>
                  <option value="upcoming">Upcoming</option>
                  <option value="closed">Closed</option>
                </select>
                <select
                  aria-label="Sort quizzes"
                  className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
                  value={sortOrder}
                  onChange={(event) => setSortOrder(event.target.value)}
                >
                  <option value="newest">Newest first</option>
                  <option value="oldest">Oldest first</option>
                  <option value="title">Title A to Z</option>
                </select>
              </div>
              {visibleExams.length === 0 ? (
                <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
                  No quizzes match those filters.
                </p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {visibleExams.map((exam) => {
                    const badge = windowBadge(exam)
                    return (
                      <li
                        key={exam.id}
                        className={
                          selected?.id === exam.id
                            ? "rounded-lg border border-primary/40 bg-primary/5 p-3"
                            : "rounded-lg border p-3"
                        }
                      >
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <Button
                            variant="ghost"
                            className="h-auto min-w-0 flex-1 flex-col items-start gap-0.5 px-2 py-1 text-left"
                            onClick={() =>
                              setSelected((current) =>
                                current?.id === exam.id ? null : exam
                              )
                            }
                          >
                            <span className="block truncate text-sm font-medium">
                              {exam.title}
                            </span>
                            <span className="block truncate text-xs text-muted-foreground">
                              {exam.id} · {exam.question_count} question
                              {exam.question_count === 1 ? "" : "s"}
                              {exam.duration_minutes
                                ? ` · ${exam.duration_minutes} min limit`
                                : ""}
                              {exam.closes_at
                                ? ` · closes ${new Date(exam.closes_at).toLocaleString()}`
                                : ""}
                            </span>
                          </Button>
                          <Badge variant={badge.variant}>{badge.label}</Badge>
                          <div className="flex flex-wrap items-center gap-1">
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => setPreviewExamId(exam.id)}
                            >
                              <EyeIcon data-icon="inline-start" />
                              Preview
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => void duplicateExam(exam)}
                            >
                              <CopyIcon data-icon="inline-start" />
                              Duplicate
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => setEditingExam(exam)}
                            >
                              <PencilIcon data-icon="inline-start" />
                              Edit
                            </Button>
                            {badge.label !== "closed" ? (
                              <ConfirmAction
                                title={`Close “${exam.title}”?`}
                                description="Students will no longer be able to start this quiz. You can reopen it later from Edit."
                                confirmLabel="Close quiz"
                                onConfirm={() => closeExam(exam)}
                              >
                                <Button size="sm" variant="outline">
                                  <LockIcon data-icon="inline-start" />
                                  Close
                                </Button>
                              </ConfirmAction>
                            ) : null}
                            <ConfirmAction
                              title={`Delete “${exam.title}”?`}
                              description="This permanently removes the quiz and its results. This action cannot be undone."
                              confirmLabel="Delete quiz"
                              onConfirm={() => deleteExam(exam)}
                            >
                              <Button
                                size="icon-sm"
                                variant="ghost"
                                aria-label={`Delete ${exam.title}`}
                              >
                                <Trash2Icon />
                              </Button>
                            </ConfirmAction>
                          </div>
                        </div>
                        {selected?.id === exam.id ? (
                          <ResultsPanel examId={exam.id} scopeId={scopeId} />
                        ) : null}
                      </li>
                    )
                  })}
                </ul>
              )}
            </>
          )}
        </CardContent>
      </Card>
      <QuizPreviewDialog
        open={previewExamId !== null}
        onOpenChange={(open) => {
          if (!open) setPreviewExamId(null)
        }}
        title={previewQuery.data?.title ?? "Quiz preview"}
        questions={previewQuery.data?.questions ?? []}
        loading={previewQuery.isPending}
        error={
          previewQuery.isError
            ? "This quiz preview is unavailable. Check the API and try again."
            : undefined
        }
      />
      <EditExamDialog
        exam={editingExam}
        classrooms={(classroomsQuery.data ?? []).map((classroom) => ({
          id: classroom.id,
          name: classroom.name,
        }))}
        onOpenChange={(open) => {
          if (!open) setEditingExam(null)
        }}
        onSaved={(updated) => {
          if (selected?.id === updated.id) setSelected(updated)
          refresh()
        }}
      />
    </div>
  )
}

export function AnalyticsOverview({ scopeId }: { scopeId: string }) {
  const [selectedExamId, setSelectedExamId] = useState("")
  const examsQuery = useQuery({
    queryKey: ["exams", scopeId],
    queryFn: api.listExams,
  })
  const focusExam =
    examsQuery.data?.find((exam) => exam.id === selectedExamId) ??
    examsQuery.data?.[0]
  useEffect(() => {
    if (!examsQuery.data?.length) {
      setSelectedExamId("")
    } else if (!examsQuery.data.some((exam) => exam.id === selectedExamId)) {
      setSelectedExamId(examsQuery.data[0].id)
    }
  }, [examsQuery.data, selectedExamId])
  const resultsQuery = useQuery({
    queryKey: ["exam-results", scopeId, focusExam?.id],
    queryFn: () => api.examResults(focusExam?.id ?? ""),
    enabled: Boolean(focusExam),
    refetchInterval: 15_000,
  })

  if (examsQuery.isPending || (focusExam && resultsQuery.isPending)) {
    return (
      <div className="grid gap-4 md:grid-cols-2">
        {[0, 1, 2, 3].map((index) => (
          <Skeleton key={index} className="h-32 rounded-xl" />
        ))}
      </div>
    )
  }

  if (examsQuery.isError) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Analytics unavailable</EmptyTitle>
          <EmptyDescription>
            Load the exam API to build a class snapshot.
          </EmptyDescription>
        </EmptyHeader>
        <Button variant="outline" onClick={() => void examsQuery.refetch()}>
          Retry analytics
        </Button>
      </Empty>
    )
  }

  if (!focusExam) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Nothing to analyze yet</EmptyTitle>
          <EmptyDescription>
            Publish a quiz and student results will appear here.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  if (resultsQuery.isError || !resultsQuery.data) {
    return (
      <div className="flex flex-col gap-4">
        <Alert>
          <BarChart3Icon />
          <AlertTitle>Quiz catalog is ready, results are not</AlertTitle>
          <AlertDescription>
            Analytics uses the existing exam-results endpoint. It may be
            unavailable on a standby node or without teacher access.
          </AlertDescription>
        </Alert>
        <label htmlFor="analytics-error-exam">Choose another quiz</label>
        <select
          id="analytics-error-exam"
          value={focusExam.id}
          onChange={(event) => setSelectedExamId(event.target.value)}
          className="h-9 rounded-lg border bg-background px-3 text-sm"
        >
          {examsQuery.data.map((exam) => (
            <option key={exam.id} value={exam.id}>
              {exam.title}
            </option>
          ))}
        </select>
        <Button variant="outline" onClick={() => void resultsQuery.refetch()}>
          Retry results
        </Button>
        <QuestionAnalytics stats={[]} />
      </div>
    )
  }

  const results = resultsQuery.data
  const average = results.average_pct ?? 0
  const openCount = examsQuery.data.filter(
    (exam) => windowBadge(exam).label === "open"
  ).length

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <AnalyticsMetric
          icon={<FilePlus2Icon />}
          label="Published"
          value={String(examsQuery.data.length)}
          detail={`${openCount} open now`}
        />
        <AnalyticsMetric
          icon={<BarChart3Icon />}
          label="Selected average"
          value={results.average_pct === null ? "—" : `${average}%`}
          detail={focusExam.title}
        />
        <AnalyticsMetric
          icon={<FilePenLineIcon />}
          label="Submissions"
          value={String(results.count)}
          detail="Selected quiz"
        />
        <AnalyticsMetric
          icon={<PlayIcon />}
          label="Selected quiz"
          value={`${focusExam.question_count}`}
          detail="questions"
        />
      </div>

      <Card>
        <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
          <div>
            <CardTitle>Class performance snapshot</CardTitle>
            <CardDescription>
              Live results for {focusExam.title}; refreshes every 15 seconds.
            </CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <label className="sr-only" htmlFor="analytics-exam">
              Choose quiz for analytics
            </label>
            <select
              id="analytics-exam"
              aria-label="Choose quiz for analytics"
              className="h-9 max-w-64 rounded-lg border border-input bg-background px-3 text-sm"
              value={focusExam.id}
              onChange={(event) => setSelectedExamId(event.target.value)}
            >
              {examsQuery.data.map((exam) => (
                <option key={exam.id} value={exam.id}>
                  {exam.title}
                </option>
              ))}
            </select>
            <Button
              size="sm"
              variant="outline"
              nativeButton={false}
              render={<Link to="/teacher" />}
            >
              Manage quizzes
            </Button>
          </div>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between gap-3">
              <p className="text-sm font-medium">Average score</p>
              <span className="text-sm text-muted-foreground tabular-nums">
                {results.average_pct === null
                  ? "No submissions yet"
                  : `${average}%`}
              </span>
            </div>
            <Progress
              aria-label="Average assessment score"
              value={results.average_pct === null ? null : average}
            />
          </div>
          {results.submissions.length === 0 ? (
            <Empty className="min-h-40 border border-dashed">
              <EmptyHeader>
                <EmptyTitle>Waiting for the first submission</EmptyTitle>
                <EmptyDescription>
                  When learners submit, their scores will be summarized here.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Learner</TableHead>
                  <TableHead className="text-right">Score</TableHead>
                  <TableHead className="text-right">Percent</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {results.submissions.slice(0, 5).map((row) => (
                  <TableRow key={row.student_id}>
                    <TableCell className="font-medium">
                      {row.student_name || row.student_id}
                    </TableCell>
                    <TableCell className="text-right">
                      {row.score}/{row.total}
                    </TableCell>
                    <TableCell className="text-right text-muted-foreground">
                      {row.total > 0
                        ? `${Math.round((row.score / row.total) * 100)}%`
                        : "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <QuestionAnalytics stats={results.question_stats} />
    </div>
  )
}

function AnalyticsMetric({
  icon,
  label,
  value,
  detail,
}: {
  icon: React.ReactNode
  label: string
  value: string
  detail: string
}) {
  return (
    <Card size="sm">
      <CardHeader className="flex-row items-start justify-between space-y-0">
        <CardDescription>{label}</CardDescription>
        <span className="text-muted-foreground [&_svg]:size-4">{icon}</span>
      </CardHeader>
      <CardContent>
        <p className="text-xl font-semibold tracking-tight">{value}</p>
        <p className="truncate text-xs text-muted-foreground">{detail}</p>
      </CardContent>
    </Card>
  )
}

function QuestionAnalytics({ stats }: { stats: QuestionStats[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Question analytics</CardTitle>
        <CardDescription>
          See where learners are succeeding or getting stuck on the latest quiz.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {stats.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            Question-level analytics will appear after the first submission.
          </p>
        ) : (
          stats.map((stat) => (
            <div
              key={stat.index}
              className="flex flex-col gap-2 rounded-lg border p-3"
            >
              <div className="flex items-start justify-between gap-3">
                <p className="min-w-0 text-sm font-medium">
                  {stat.index + 1}. {stat.question}
                </p>
                <Badge
                  variant={
                    stat.correct_pct !== null && stat.correct_pct < 50
                      ? "destructive"
                      : "secondary"
                  }
                >
                  {stat.correct_pct === null
                    ? "no answers"
                    : `${stat.correct_pct}% correct`}
                </Badge>
              </div>
              <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
                <span>
                  {stat.correct}/{stat.answered} correct
                </span>
                <span>{stat.answered} answered</span>
              </div>
              <Progress
                aria-label={`Question ${stat.index + 1} correct answers`}
                value={stat.correct_pct}
              />
            </div>
          ))
        )}
      </CardContent>
    </Card>
  )
}

function ResultsPanel({
  examId,
  scopeId,
}: {
  examId: string
  scopeId: string
}) {
  const resultsQuery = useQuery({
    queryKey: ["exam-results", scopeId, examId],
    queryFn: () => api.examResults(examId),
    refetchInterval: 15_000,
  })

  if (resultsQuery.isPending) {
    return <Skeleton className="mt-3 h-24 w-full" />
  }
  if (resultsQuery.isError) {
    return (
      <p className="mt-3 text-xs text-destructive">
        Couldn't load results
        {resultsQuery.error instanceof ApiError
          ? ` — ${resultsQuery.error.message}`
          : ""}
        .
      </p>
    )
  }

  const results = resultsQuery.data

  return (
    <div className="mt-3 flex flex-col gap-2 border-t pt-3">
      <div className="flex items-center justify-between">
        <p className="text-sm font-medium">
          {results.count} submission{results.count === 1 ? "" : "s"}
          {results.average_pct !== null
            ? ` · class average ${results.average_pct}%`
            : ""}
        </p>
        <Button
          size="sm"
          variant="outline"
          disabled={results.submissions.length === 0}
          onClick={() => downloadResultsCsv(results)}
        >
          <DownloadIcon data-icon="inline-start" />
          CSV
        </Button>
      </div>
      {results.submissions.length === 0 ? (
        <p className="text-xs text-muted-foreground">No submissions yet.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Student</TableHead>
              <TableHead className="text-right">Score</TableHead>
              <TableHead className="text-right">Percent</TableHead>
              <TableHead className="text-right">Submitted</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {results.submissions.map((row) => (
              <TableRow key={row.student_id}>
                <TableCell className="font-medium">
                  {row.student_name || row.student_id}
                </TableCell>
                <TableCell className="text-right">
                  {row.score}/{row.total}
                </TableCell>
                <TableCell className="text-right">
                  {row.total > 0
                    ? `${Math.round((row.score / row.total) * 100)}%`
                    : "—"}
                </TableCell>
                <TableCell className="text-right text-muted-foreground">
                  {row.submitted_at
                    ? new Date(row.submitted_at).toLocaleTimeString()
                    : "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

function downloadResultsCsv(results: ExamResultsResponse): void {
  const escape = (value: string): string => `"${value.replace(/"/g, '""')}"`
  const rows: string[][] = [
    ["student", "score", "total", "percent", "submitted"],
  ]
  for (const row of results.submissions) {
    rows.push([
      row.student_name || row.student_id,
      String(row.score),
      String(row.total),
      row.total > 0 ? `${Math.round((row.score / row.total) * 100)}%` : "",
      row.submitted_at ?? "",
    ])
  }
  const csv = rows.map((row) => row.map(escape).join(",")).join("\n")
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = `${results.exam_id}-results.csv`
  anchor.click()
  URL.revokeObjectURL(url)
}

function SubmissionsPanel({ scopeId }: { scopeId: string }) {
  const mounted = useMounted()
  const [adminToken, saveAdminToken] = useAdminToken()

  const submissionsQuery = useQuery({
    queryKey: ["submissions", scopeId, adminToken],
    queryFn: () => api.submissions(adminToken),
    enabled: mounted && adminToken.length > 0,
    refetchInterval: 10_000,
  })

  if (!adminToken) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Submissions</CardTitle>
          <CardDescription>
            The raw submission feed is a platform-ops view — paste the admin
            token to unlock it.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <FieldGroup className="max-w-sm">
            <Field>
              <FieldLabel htmlFor="submissions-token">Admin token</FieldLabel>
              <InputGroup>
                <InputGroupInput
                  id="submissions-token"
                  type="password"
                  placeholder="Paste admin token"
                  value={adminToken}
                  onChange={(event) => saveAdminToken(event.target.value)}
                />
              </InputGroup>
              <FieldDescription>Saved in this browser only.</FieldDescription>
            </Field>
          </FieldGroup>
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <div className="flex flex-col gap-1">
          <CardTitle>Recent submissions</CardTitle>
          <CardDescription>Auto-refreshes every 10 seconds.</CardDescription>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void submissionsQuery.refetch()}
          disabled={submissionsQuery.isFetching}
        >
          {submissionsQuery.isFetching ? (
            <>
              <Spinner data-icon="inline-start" />
              Syncing
            </>
          ) : (
            <>
              <RefreshCwIcon data-icon="inline-start" />
              Refresh
            </>
          )}
        </Button>
      </CardHeader>
      <CardContent>
        {!mounted || submissionsQuery.isPending ? (
          <div className="flex flex-col gap-2">
            {[0, 1, 2].map((index) => (
              <Skeleton key={index} className="h-10 w-full" />
            ))}
          </div>
        ) : submissionsQuery.isError ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>Couldn't load submissions</EmptyTitle>
              <EmptyDescription>
                Check the admin token and try again.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : submissionsQuery.data.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No submissions yet</EmptyTitle>
              <EmptyDescription>
                They'll appear here the moment students submit.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Student</TableHead>
                <TableHead>Exam</TableHead>
                <TableHead>Answers</TableHead>
                <TableHead className="text-right">Version</TableHead>
                <TableHead className="text-right">Submitted</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {submissionsQuery.data.map((submission) => (
                <TableRow key={`${submission.pk}/${submission.sk}`}>
                  <TableCell className="font-medium">
                    {submission.sk.replace("STUDENT#", "")}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {submission.pk.replace("SUBMISSION#", "")}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {Object.entries(submission.answers)
                      .map(
                        ([index, choice]) =>
                          `${Number(index) + 1}→${optionLetter(choice)}`
                      )
                      .join("  ")}
                  </TableCell>
                  <TableCell className="text-right">
                    v{submission.version}
                  </TableCell>
                  <TableCell className="text-right text-muted-foreground">
                    {submission.updated_at_iso.slice(11, 19)} UTC
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
