import { useEffect, useState } from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
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
  PlusIcon,
  RadioIcon,
  RefreshCwIcon,
  SaveIcon,
  SendIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { useAdminToken } from "@/lib/admin-token"
import { useQuizDrafts, removeQuizDraft, saveQuizDraft } from "@/lib/quiz-drafts"
import { removeQuestionFromBank, saveQuestionToBank, useQuestionBank } from "@/lib/question-bank"
import { useBoardHistory } from "@/lib/board-history"
import { useSelectedClassroom } from "@/lib/classrooms"
import { requireTeacher } from "@/lib/guard"
import type { ExamDraft, ExamMeta, ExamResultsResponse, QuestionDraft, QuestionStats, QuizDraft } from "@/lib/types"
import { useMounted } from "@/hooks/use-mounted"
import { AppShell } from "@/components/layout/app-shell"
import { QuizPreviewDialog } from "@/components/quiz-preview-dialog"
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
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { Progress } from "@/components/ui/progress"
import { Input } from "@/components/ui/input"

export const Route = createFileRoute("/teacher")({
  ssr: false,
  beforeLoad: ({ location }) => requireTeacher(location),
  component: TeacherPage,
  head: () => ({ meta: [{ title: "Quizzes · Slate" }] }),
})

interface BuilderQuestion extends QuestionDraft {
  key: string
}

let questionCounter = 0

function blankQuestion(): BuilderQuestion {
  questionCounter += 1
  return { key: `q-${questionCounter}`, question: "", options: ["", ""], answer: 0 }
}

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

function TeacherPage() {
  const [tab, setTab] = useState("create")
  const [draftSeed, setDraftSeed] = useState<ExamDraft | null>(null)

  return (
    <AppShell
      title="Teacher studio"
      description="Author quizzes and review submissions"
      actions={
        <Tabs value={tab} onValueChange={(value) => setTab(value)}>
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
              key={draftSeed?.id ?? "new-quiz"}
              initialDraft={draftSeed}
              onPublished={() => {
                setDraftSeed(null)
                setTab("manage")
              }}
            />
          </TabsContent>
          <TabsContent value="manage">
            <ExamManager
              onDuplicate={(draft) => {
                setDraftSeed(draft)
                setTab("create")
              }}
            />
          </TabsContent>
          <TabsContent value="analytics">
            <AnalyticsOverview />
          </TabsContent>
          <TabsContent value="submissions">
            <SubmissionsPanel />
          </TabsContent>
        </Tabs>
      </div>
    </AppShell>
  )
}

function windowBadge(meta: ExamMeta): { label: string; variant: "default" | "secondary" | "destructive" } {
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
          <p className="truncate text-sm font-semibold">{classroom?.name ?? "No class selected"}</p>
          <p className="truncate text-xs text-muted-foreground">
            {classroom ? `${classroom.members.length} roster members` : "Create one from the dashboard"}
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
            <p className="truncate text-sm font-semibold">{classroom?.active_room_id ?? "Not started"}</p>
            <p className="truncate text-xs text-muted-foreground">
              {latestRoom ? `${latestRoom.visits} local visit${latestRoom.visits === 1 ? "" : "s"}` : "Board metadata is local"}
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
          <p className="text-sm font-semibold">{classroom ? "Available" : "Waiting"}</p>
            <p className="truncate text-xs text-muted-foreground">Roster API is connected; live presence stays in the board.</p>
        </CardContent>
      </Card>
    </div>
  )
}

function ExamManager({ onDuplicate }: { onDuplicate: (draft: ExamDraft) => void }) {
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<ExamMeta | null>(null)
  const [previewExamId, setPreviewExamId] = useState<string | null>(null)

  const examsQuery = useQuery({
    queryKey: ["exams"],
    queryFn: api.listExams,
  })

  const previewQuery = useQuery({
    queryKey: ["exam-preview", previewExamId],
    queryFn: () => api.previewExam(previewExamId ?? ""),
    enabled: previewExamId !== null,
  })

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: ["exams"] })
  }

  async function closeExam(exam: ExamMeta) {
    try {
      await api.closeExam(exam.id)
      toast.success(`Closed "${exam.title}"`)
      refresh()
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : "Could not close quiz")
    }
  }

  async function deleteExam(exam: ExamMeta) {
    try {
      await api.deleteExam(exam.id)
      toast.success(`Deleted "${exam.title}"`)
      if (selected?.id === exam.id) setSelected(null)
      refresh()
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : "Could not delete quiz")
    }
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
      })
      toast.success(`Created a draft copy of "${exam.title}"`)
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : "Could not duplicate quiz")
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div className="flex flex-col gap-1">
            <CardTitle>Your quizzes</CardTitle>
            <CardDescription>Preview, duplicate, close, or remove a published quiz.</CardDescription>
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
                <EmptyDescription>Publish one from the Create tab.</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <ul className="flex flex-col gap-2">
              {examsQuery.data.map((exam) => {
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
                          setSelected((current) => (current?.id === exam.id ? null : exam))
                        }
                      >
                        <span className="block truncate text-sm font-medium">{exam.title}</span>
                        <span className="block truncate text-xs text-muted-foreground">
                          {exam.id} · {exam.question_count} question{exam.question_count === 1 ? "" : "s"}
                          {exam.duration_minutes ? ` · ${exam.duration_minutes} min limit` : ""}
                          {exam.closes_at ? ` · closes ${new Date(exam.closes_at).toLocaleString()}` : ""}
                        </span>
                      </Button>
                      <Badge variant={badge.variant}>{badge.label}</Badge>
                      <div className="flex flex-wrap items-center gap-1">
                        <Button size="sm" variant="ghost" onClick={() => setPreviewExamId(exam.id)}>
                          <EyeIcon data-icon="inline-start" />
                          Preview
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => void duplicateExam(exam)}>
                          <CopyIcon data-icon="inline-start" />
                          Duplicate
                        </Button>
                        {badge.label !== "closed" ? (
                          <Button size="sm" variant="outline" onClick={() => void closeExam(exam)}>
                            <LockIcon data-icon="inline-start" />
                            Close
                          </Button>
                        ) : null}
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          aria-label={`Delete ${exam.title}`}
                          onClick={() => void deleteExam(exam)}
                        >
                          <Trash2Icon />
                        </Button>
                      </div>
                    </div>
                    {selected?.id === exam.id ? <ResultsPanel examId={exam.id} /> : null}
                  </li>
                )
              })}
            </ul>
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
        error={previewQuery.isError ? "This quiz preview is unavailable. Check the API and try again." : undefined}
      />
    </div>
  )
}

function AnalyticsOverview() {
  const examsQuery = useQuery({
    queryKey: ["exams"],
    queryFn: api.listExams,
  })
  const focusExam = examsQuery.data?.[0]
  const resultsQuery = useQuery({
    queryKey: ["exam-results", focusExam?.id],
    queryFn: () => api.examResults(focusExam?.id ?? ""),
    enabled: Boolean(focusExam),
    refetchInterval: 15_000,
  })

  if (examsQuery.isPending || (focusExam && resultsQuery.isPending)) {
    return (
      <div className="grid gap-4 md:grid-cols-2">
        {[0, 1, 2, 3].map((index) => <Skeleton key={index} className="h-32 rounded-xl" />)}
      </div>
    )
  }

  if (examsQuery.isError) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Analytics unavailable</EmptyTitle>
          <EmptyDescription>Load the exam API to build a class snapshot.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  if (!focusExam) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Nothing to analyze yet</EmptyTitle>
          <EmptyDescription>Publish a quiz and student results will appear here.</EmptyDescription>
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
            Analytics uses the existing exam-results endpoint. It may be unavailable on a standby node or without teacher access.
          </AlertDescription>
        </Alert>
        <QuestionAnalytics stats={[]} />
      </div>
    )
  }

  const results = resultsQuery.data
  const average = results.average_pct ?? 0
  const openCount = examsQuery.data.filter((exam) => windowBadge(exam).label === "open").length

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <AnalyticsMetric icon={<FilePlus2Icon />} label="Published" value={String(examsQuery.data.length)} detail={`${openCount} open now`} />
        <AnalyticsMetric icon={<BarChart3Icon />} label="Latest average" value={`${average}%`} detail={focusExam.title} />
        <AnalyticsMetric icon={<FilePenLineIcon />} label="Submissions" value={String(results.count)} detail="Latest published quiz" />
        <AnalyticsMetric icon={<PlayIcon />} label="Latest quiz" value={`${focusExam.question_count}`} detail="questions" />
      </div>

      <Card>
        <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
          <div>
            <CardTitle>Class performance snapshot</CardTitle>
            <CardDescription>Live results for {focusExam.title}; refreshes every 15 seconds.</CardDescription>
          </div>
          <Button size="sm" variant="outline" nativeButton={false} render={<Link to="/teacher" />}>
            Manage quizzes
          </Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between gap-3">
              <p className="text-sm font-medium">Average score</p>
              <span className="text-sm tabular-nums text-muted-foreground">{average}%</span>
            </div>
            <Progress value={average} />
          </div>
          {results.submissions.length === 0 ? (
            <Empty className="min-h-40 border border-dashed">
              <EmptyHeader>
                <EmptyTitle>Waiting for the first submission</EmptyTitle>
                <EmptyDescription>When learners submit, their scores will be summarized here.</EmptyDescription>
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
                    <TableCell className="font-medium">{row.student_name || row.student_id}</TableCell>
                    <TableCell className="text-right">{row.score}/{row.total}</TableCell>
                    <TableCell className="text-right text-muted-foreground">
                      {row.total > 0 ? `${Math.round((row.score / row.total) * 100)}%` : "—"}
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
        <CardDescription>See where learners are succeeding or getting stuck on the latest quiz.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {stats.length === 0 ? (
          <p className="text-sm text-muted-foreground">Question-level analytics will appear after the first submission.</p>
        ) : stats.map((stat) => (
          <div key={stat.index} className="flex flex-col gap-2 rounded-lg border p-3">
            <div className="flex items-start justify-between gap-3">
              <p className="min-w-0 text-sm font-medium">{stat.index + 1}. {stat.question}</p>
              <Badge variant={stat.correct_pct !== null && stat.correct_pct < 50 ? "destructive" : "secondary"}>
                {stat.correct_pct === null ? "no answers" : `${stat.correct_pct}% correct`}
              </Badge>
            </div>
            <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
              <span>{stat.correct}/{stat.answered} correct</span>
              <span>{stat.answered} answered</span>
            </div>
            <Progress value={stat.correct_pct ?? 0} />
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

function ResultsPanel({ examId }: { examId: string }) {
  const resultsQuery = useQuery({
    queryKey: ["exam-results", examId],
    queryFn: () => api.examResults(examId),
    refetchInterval: 15_000,
  })

  if (resultsQuery.isPending) {
    return <Skeleton className="mt-3 h-24 w-full" />
  }
  if (resultsQuery.isError) {
    return (
      <p className="mt-3 text-xs text-destructive">
        Couldn't load results{resultsQuery.error instanceof ApiError
          ? ` — ${resultsQuery.error.message}`
          : ""}.
      </p>
    )
  }

  const results = resultsQuery.data

  return (
    <div className="mt-3 flex flex-col gap-2 border-t pt-3">
      <div className="flex items-center justify-between">
        <p className="text-sm font-medium">
          {results.count} submission{results.count === 1 ? "" : "s"}
          {results.average_pct !== null ? ` · class average ${results.average_pct}%` : ""}
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
                <TableCell className="font-medium">{row.student_name || row.student_id}</TableCell>
                <TableCell className="text-right">{row.score}/{row.total}</TableCell>
                <TableCell className="text-right">
                  {row.total > 0 ? `${Math.round((row.score / row.total) * 100)}%` : "—"}
                </TableCell>
                <TableCell className="text-right text-muted-foreground">
                  {row.submitted_at ? new Date(row.submitted_at).toLocaleTimeString() : "—"}
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
  const rows: string[][] = [["student", "score", "total", "percent", "submitted"]]
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

function QuizBuilder({
  initialDraft,
  onPublished,
}: {
  initialDraft: ExamDraft | null
  onPublished: () => void
}) {
  const queryClient = useQueryClient()
  const drafts = useQuizDrafts()
  const questionBank = useQuestionBank()
  const serverDraftsQuery = useQuery({
    queryKey: ["quiz-drafts"],
    queryFn: api.listQuizDrafts,
    retry: false,
  })
  const [title, setTitle] = useState("")
  const [customId, setCustomId] = useState("")
  const [questions, setQuestions] = useState<BuilderQuestion[]>(() => [blankQuestion()])
  const [draftId, setDraftId] = useState<string | undefined>(undefined)
  const [serverDraftId, setServerDraftId] = useState<string | undefined>(initialDraft?.id)
  const [previewOpen, setPreviewOpen] = useState(false)

  useEffect(() => {
    if (!initialDraft) return
    setTitle(initialDraft.title)
    setCustomId(initialDraft.id ?? "")
    setServerDraftId(initialDraft.id)
    setQuestions(
      initialDraft.questions.map((question, index) => ({
        ...question,
        options: [...question.options],
        key: `seed-${index}-${Date.now()}`,
      })),
    )
  }, [initialDraft])

  function patchQuestion(key: string, patch: Partial<BuilderQuestion>) {
    setQuestions((current) =>
      current.map((question) => (question.key === key ? { ...question, ...patch } : question)),
    )
  }

  function addOption(key: string) {
    setQuestions((current) =>
      current.map((question) =>
        question.key === key && question.options.length < 6
          ? { ...question, options: [...question.options, ""] }
          : question,
      ),
    )
  }

  function removeOption(key: string, index: number) {
    setQuestions((current) =>
      current.map((question) => {
        if (question.key !== key || question.options.length <= 2) return question
        const options = question.options.filter((_, position) => position !== index)
        return {
          ...question,
          options,
          answer: Math.min(question.answer, options.length - 1),
        }
      }),
    )
  }

  function buildDraft(): ExamDraft {
    return {
      id: customId.trim() || undefined,
      title: title.trim(),
      questions: questions.map(({ question, options, answer, points, feedback }) => ({
        question: question.trim(),
        options: options.map((option) => option.trim()),
        answer,
        points,
        feedback: feedback?.trim() || undefined,
      })),
    }
  }

  async function saveDraft(): Promise<void> {
    const nextDraft = {
      ...buildDraft(),
      title: title.trim() || "Untitled draft",
    }
    const saved = saveQuizDraft(nextDraft, draftId)
    setDraftId(saved.draft_id)
    try {
      let synced: QuizDraft
      if (serverDraftId) {
        try {
          synced = await api.updateQuizDraft(serverDraftId, nextDraft)
        } catch (error) {
          if (!(error instanceof ApiError) || error.status !== 404) throw error
          synced = await api.createQuizDraft(nextDraft)
        }
      } else {
        synced = await api.createQuizDraft(nextDraft)
      }
      setServerDraftId(synced.id)
      void queryClient.invalidateQueries({ queryKey: ["quiz-drafts"] })
      toast.success("Draft saved", { description: "Synced to your teacher workspace." })
    } catch (error) {
      toast.warning("Draft saved locally", {
        description: error instanceof ApiError ? error.message : "The server could not be reached.",
      })
    }
  }

  function loadDraft(draft: ExamDraft & { draft_id: string }): void {
    setDraftId(draft.draft_id)
    setServerDraftId(undefined)
    setTitle(draft.title)
    setCustomId(draftCustomId(draft.id))
    setQuestions(
      draft.questions.map((question, index) => ({
        ...question,
        options: [...question.options],
        key: `saved-${index}-${Date.now()}`,
      })),
    )
    toast.info("Draft loaded")
  }

  function loadServerDraft(draft: QuizDraft): void {
    setDraftId(undefined)
    setServerDraftId(draft.id)
    setTitle(draft.title)
    setCustomId(draft.id)
    setQuestions(
      draft.questions.map((question, index) => ({
        ...question,
        options: [...question.options],
        key: `server-${index}-${Date.now()}`,
      })),
    )
    toast.info("Server draft loaded")
  }

  function saveQuestion(question: BuilderQuestion): void {
    if (!question.question.trim() || question.options.filter((option) => option.trim()).length < 2) {
      toast.error("Add a prompt and two options before saving to the bank")
      return
    }
    saveQuestionToBank({
      question: question.question.trim(),
      options: question.options.map((option) => option.trim()),
      answer: question.answer,
      points: question.points,
      feedback: question.feedback?.trim() || undefined,
    })
    toast.success("Question saved to the bank")
  }

  function useBankQuestion(question: typeof questionBank[number]): void {
    setQuestions((current) => [
      ...current,
      { ...question, options: [...question.options], key: `bank-${question.id}-${Date.now()}` },
    ])
    toast.info("Question added to this quiz")
  }

  const publishMutation = useMutation({
    mutationFn: async () => {
      const nextDraft = { ...buildDraft(), title: title.trim() }
      let draftIdToPublish = serverDraftId
      if (serverDraftId) {
        try {
          await api.updateQuizDraft(serverDraftId, nextDraft)
        } catch (error) {
          if (!(error instanceof ApiError) || error.status !== 404) throw error
          const created = await api.createQuizDraft(nextDraft)
          setServerDraftId(created.id)
          draftIdToPublish = created.id
        }
        if (!draftIdToPublish) throw new Error("draft id is required to publish")
        return api.publishQuizDraft(draftIdToPublish)
      }
      const created = await api.createQuizDraft(nextDraft)
      setServerDraftId(created.id)
      return api.publishQuizDraft(created.id)
    },
    onSuccess: (created) => {
      toast.success(`Published "${created.title}"`, {
        description: `${created.question_count} questions · id ${created.id}`,
      })
      void queryClient.invalidateQueries({ queryKey: ["exams"] })
      void queryClient.invalidateQueries({ queryKey: ["quiz-drafts"] })
      setTitle("")
      setCustomId("")
      setQuestions([blankQuestion()])
      setDraftId(undefined)
      onPublished()
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 401) {
        toast.error("Please sign in again to publish")
        return
      }
      toast.error(error instanceof ApiError ? error.message : "Could not publish quiz")
    },
  })

  const titleReady = title.trim().length > 0
  const questionsReady =
    questions.length > 0 &&
    questions.every(
      (question) =>
        question.question.trim().length > 0 &&
        question.options.filter((option) => option.trim().length > 0).length >= 2 &&
        question.answer >= 0 &&
        question.answer < question.options.length,
    )
  const canPublish = titleReady && questionsReady && !publishMutation.isPending

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Quiz details</CardTitle>
          <CardDescription>Students only see the title and the questions.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <FieldGroup>
            <Field data-invalid={!titleReady && title.length > 0 ? true : undefined}>
              <FieldLabel htmlFor="quiz-title">Title</FieldLabel>
              <InputGroup>
                <InputGroupInput
                  id="quiz-title"
                  placeholder="CS101 — Week 4 recap"
                  value={title}
                  onChange={(event) => setTitle(event.target.value)}
                />
              </InputGroup>
            </Field>
            <Field>
              <FieldLabel htmlFor="quiz-id">Custom ID (optional)</FieldLabel>
              <InputGroup>
                <InputGroupInput
                  id="quiz-id"
                  placeholder="auto-generated from the title"
                  value={customId}
                  onChange={(event) => setCustomId(event.target.value)}
                  autoCapitalize="none"
                  spellCheck={false}
                />
              </InputGroup>
              <FieldDescription>A slug; conflicts are rejected with a 409.</FieldDescription>
            </Field>
          </FieldGroup>
        </CardContent>
      </Card>

      <Card size="sm">
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div className="flex min-w-0 flex-col gap-1">
            <CardTitle className="flex items-center gap-2 text-sm">
              <FilePenLineIcon data-icon="inline-start" />
              Local drafts
            </CardTitle>
            <CardDescription>Save locally for quick recovery, or sync the draft to your teacher workspace.</CardDescription>
          </div>
          <Badge variant="secondary">{drafts.length}</Badge>
        </CardHeader>
        {drafts.length > 0 ? (
          <CardContent className="flex flex-col gap-2">
            {drafts.slice(0, 3).map((draft) => (
              <div key={draft.draft_id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border p-2">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{displayDraftTitle(draft.title)}</p>
                  <p className="text-xs text-muted-foreground">Updated {new Date(draft.updated_at).toLocaleString()}</p>
                </div>
                <div className="flex items-center gap-1">
                  <Button size="sm" variant="outline" onClick={() => loadDraft(draft)}>
                    Load
                  </Button>
                  <Button size="icon-sm" variant="ghost" aria-label={`Delete ${draftDeleteLabel(draft.title)}`} onClick={() => removeQuizDraft(draft.draft_id)}>
                    <Trash2Icon />
                  </Button>
                </div>
              </div>
            ))}
          </CardContent>
        ) : null}
        {serverDraftsQuery.data && serverDraftsQuery.data.length > 0 ? (
          <CardContent className="border-t pt-3">
            <p className="mb-2 text-xs font-medium text-muted-foreground">Server drafts</p>
            <div className="flex flex-wrap gap-2">
              {serverDraftsQuery.data.slice(0, 6).map((draft) => (
                <Button key={draft.id} size="sm" variant="outline" onClick={() => loadServerDraft(draft)}>
                  {draft.title}
                </Button>
              ))}
            </div>
          </CardContent>
        ) : null}
      </Card>

      <Card size="sm">
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div className="flex min-w-0 flex-col gap-1">
            <CardTitle className="text-sm">Question bank</CardTitle>
            <CardDescription>Reuse strong questions across quizzes in this browser.</CardDescription>
          </div>
          <Badge variant="secondary">{questionBank.length}</Badge>
        </CardHeader>
        {questionBank.length > 0 ? (
          <CardContent className="flex flex-col gap-2">
            {questionBank.slice(0, 6).map((question) => (
              <div key={question.id} className="flex items-center justify-between gap-2 rounded-lg border p-2">
                <p className="min-w-0 truncate text-sm">{question.question}</p>
                <div className="flex shrink-0 items-center gap-1">
                  <Button size="sm" variant="outline" onClick={() => useBankQuestion(question)}>Use</Button>
                  <Button size="icon-sm" variant="ghost" aria-label={`Delete ${question.question}`} onClick={() => removeQuestionFromBank(question.id)}>
                    <Trash2Icon />
                  </Button>
                </div>
              </div>
            ))}
          </CardContent>
        ) : (
          <CardContent>
            <p className="text-xs text-muted-foreground">Save any completed question here to reuse it later.</p>
          </CardContent>
        )}
      </Card>

      {questions.map((question, index) => (
        <Card key={question.key}>
          <CardHeader className="flex-row items-start justify-between space-y-0">
            <div className="flex flex-col gap-1">
              <CardTitle className="text-base">Question {index + 1}</CardTitle>
              <CardDescription>Multiple choice; pick the correct option below.</CardDescription>
            </div>
            {questions.length > 1 ? (
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Remove question ${index + 1}`}
                onClick={() =>
                  setQuestions((current) => current.filter((q) => q.key !== question.key))
                }
              >
                <Trash2Icon />
              </Button>
            ) : null}
            <Button size="sm" variant="outline" onClick={() => saveQuestion(question)}>
              <SaveIcon data-icon="inline-start" />
              Bank
            </Button>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <Field>
              <FieldLabel htmlFor={`question-text-${question.key}`}>Prompt</FieldLabel>
              <Textarea
                id={`question-text-${question.key}`}
                placeholder="What does RPO measure?"
                value={question.question}
                onChange={(event) => patchQuestion(question.key, { question: event.target.value })}
              />
            </Field>
            <FieldGroup>
              {question.options.map((option, optionIndex) => (
                <Field key={`${question.key}-${optionIndex}`}>
                  <FieldLabel htmlFor={`option-${question.key}-${optionIndex}`}>
                    Option {optionLetter(optionIndex)}
                    {optionIndex === question.answer ? (
                      <Badge variant="default" className="ml-2">
                        correct
                      </Badge>
                    ) : null}
                  </FieldLabel>
                  <div className="flex items-center gap-2">
                    <InputGroup className="flex-1">
                      <InputGroupInput
                        id={`option-${question.key}-${optionIndex}`}
                        placeholder={`Option ${optionLetter(optionIndex)}`}
                        value={option}
                        onChange={(event) => {
                          const options = [...question.options]
                          options[optionIndex] = event.target.value
                          patchQuestion(question.key, { options })
                        }}
                      />
                      <InputGroupAddon align="inline-end">
                        <InputGroupButton
                          aria-label={`Mark option ${optionLetter(optionIndex)} as correct`}
                          onClick={() => patchQuestion(question.key, { answer: optionIndex })}
                          variant={optionIndex === question.answer ? "default" : "ghost"}
                        >
                          <EyeIcon />
                        </InputGroupButton>
                        {question.options.length > 2 ? (
                          <InputGroupButton
                            aria-label={`Remove option ${optionLetter(optionIndex)}`}
                            onClick={() => removeOption(question.key, optionIndex)}
                          >
                            <Trash2Icon />
                          </InputGroupButton>
                        ) : null}
                      </InputGroupAddon>
                    </InputGroup>
                  </div>
                </Field>
              ))}
            </FieldGroup>
            {question.options.length < 6 ? (
              <Button variant="outline" size="sm" onClick={() => addOption(question.key)}>
                <PlusIcon data-icon="inline-start" />
                Add option
              </Button>
            ) : null}
            <FieldGroup className="grid gap-4 sm:grid-cols-[8rem_minmax(0,1fr)]">
              <Field>
                <FieldLabel htmlFor={`points-${question.key}`}>Points</FieldLabel>
                <Input
                  id={`points-${question.key}`}
                  type="number"
                  min={0}
                  max={1000}
                  value={question.points ?? 1}
                  onChange={(event) => patchQuestion(question.key, { points: Number(event.target.value) || 0 })}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor={`feedback-${question.key}`}>Feedback (optional)</FieldLabel>
                <Textarea
                  id={`feedback-${question.key}`}
                  placeholder="Explain why the answer is correct."
                  value={question.feedback ?? ""}
                  onChange={(event) => patchQuestion(question.key, { feedback: event.target.value })}
                />
              </Field>
            </FieldGroup>
          </CardContent>
        </Card>
      ))}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => setQuestions((current) => [...current, blankQuestion()])}>
            <PlusIcon data-icon="inline-start" />
            Add question
          </Button>
          <Button variant="outline" onClick={() => void saveDraft()}>
            <SaveIcon data-icon="inline-start" />
            Save draft
          </Button>
          <Button variant="ghost" onClick={() => setPreviewOpen(true)}>
            <EyeIcon data-icon="inline-start" />
            Preview
          </Button>
        </div>
        <Button disabled={!canPublish} onClick={() => publishMutation.mutate()}>
          {publishMutation.isPending ? (
            <>
              <Spinner data-icon="inline-start" />
              Publishing…
            </>
          ) : (
            <>
              <SendIcon data-icon="inline-start" />
              Publish quiz
            </>
          )}
        </Button>
      </div>
      <QuizPreviewDialog
        open={previewOpen}
        onOpenChange={setPreviewOpen}
        title={title}
        questions={questions}
      />
    </div>
  )
}

function SubmissionsPanel() {
  const mounted = useMounted()
  const [adminToken, saveAdminToken] = useAdminToken()

  const submissionsQuery = useQuery({
    queryKey: ["submissions", adminToken],
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
              <EmptyDescription>Check the admin token and try again.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : submissionsQuery.data.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No submissions yet</EmptyTitle>
              <EmptyDescription>They'll appear here the moment students submit.</EmptyDescription>
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
                      .map(([index, choice]) => `${Number(index) + 1}→${optionLetter(choice)}`)
                      .join("  ")}
                  </TableCell>
                  <TableCell className="text-right">v{submission.version}</TableCell>
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
