import { useEffect, useMemo, useRef, useState } from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  CheckCircle2Icon,
  CheckIcon,
  ClockIcon,
  CloudOffIcon,
  CloudUploadIcon,
  CopyIcon,
  DownloadIcon,
  FlagIcon,
  ListChecksIcon,
  PrinterIcon,
  RotateCcwIcon,
  SendIcon,
  XCircleIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { examState } from "@/lib/types"
import type {
  AttemptSnapshot,
  ExamMeta,
  ResultResponse,
  SubmitResult,
} from "@/lib/types"
import { getAuth, useAuth } from "@/lib/auth"
import {
  clearExamDraft,
  clearPendingSubmission,
  createOrLoadPendingSubmission,
  findPendingSubmissionForExam,
  readExamDraft,
  readPendingSubmission,
  isTransientSubmissionError,
  setPendingSubmissionStatus,
  submitWithRetry,
  writeExamDraft,
} from "@/lib/pending-submission"
import type { ExamScope, PendingSubmission } from "@/lib/pending-submission"
import { requireAuth } from "@/lib/guard"
import { useMounted } from "@/hooks/use-mounted"
import { useOnlineStatus } from "@/hooks/use-online-status"
import {
  readQuizProgress,
  toggleFlaggedQuestion,
  visibleQuestionPositions,
  writeQuizProgress,
} from "@/lib/quiz-progress"
import { formatResultReceipt } from "@/lib/result-receipt"
import {
  canRetakeAssessment,
  isAssessmentWindowOpen,
  isResultForCurrentAttempt,
} from "@/lib/assessment-history"
import { AppShell } from "@/components/layout/app-shell"
import { ConfirmAction } from "@/components/confirm-action"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
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
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Progress } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export const Route = createFileRoute("/quiz/$examId")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: QuizPage,
  head: () => ({ meta: [{ title: "Timed assessment · Continuum" }] }),
})

function clockLabel(msRemaining: number): string {
  const seconds = Math.max(0, Math.ceil(msRemaining / 1000))
  const minutes = Math.floor(seconds / 60)
  return `${String(minutes).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`
}

function examScope(
  examId: string,
  attemptNumber = 1,
  userId = getAuth().user?.user_id ?? "anonymous"
): ExamScope {
  return {
    userId,
    examId,
    attemptNumber,
  }
}

export function loadDraftAnswers(
  examId: string,
  attemptNumber = 1
): Partial<Record<number, number>> {
  return readExamDraft(examScope(examId, attemptNumber))
}

export function serialiseAnswerMap(
  answers: Partial<Record<number, number>>
): Record<string, number> {
  const serialised: Record<string, number> = {}
  for (const [index, choice] of Object.entries(answers)) {
    if (typeof choice === "number") serialised[index] = choice
  }
  return serialised
}

export function saveFailureStatus(online: boolean): "error" | "offline" {
  return online ? "error" : "offline"
}

export function applyRadioChoice(
  value: unknown,
  onChoice: (choice: number) => void
): void {
  if (typeof value === "string") onChoice(Number(value))
}

function QuizPage() {
  const { examId } = Route.useParams()
  const mounted = useMounted()
  const userId = useAuth().user?.user_id ?? "anonymous"
  const resultKey = ["result", userId, examId] as const
  const pendingForExam = findPendingSubmissionForExam(userId, examId)

  const resultQuery = useQuery({
    queryKey: resultKey,
    queryFn: () => api.myResult(examId),
    enabled: mounted,
    retry: false,
    staleTime: Infinity,
  })
  const infoQuery = useQuery({
    queryKey: ["exam-info", userId, examId],
    queryFn: () => api.examInfo(examId),
    enabled: mounted,
    retry: false,
    staleTime: 30_000,
  })

  if (!mounted || resultQuery.isPending || infoQuery.isPending) {
    return (
      <AppShell title="Quiz" padded>
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
          <Skeleton className="h-10 w-2/3 rounded-lg" />
          <Skeleton className="h-64 w-full rounded-xl" />
        </div>
      </AppShell>
    )
  }

  if (
    resultQuery.data &&
    isResultForCurrentAttempt(resultQuery.data, infoQuery.data)
  ) {
    const receiptRecovered = Boolean(
      pendingForExam && resultQuery.data.request_id === pendingForExam.requestId
    )
    return (
      <ResultView
        examId={examId}
        userId={userId}
        result={resultQuery.data}
        meta={infoQuery.data}
        receiptRecovered={receiptRecovered}
        pendingRequestId={pendingForExam?.requestId}
        attemptNumber={pendingForExam?.attemptNumber}
        unresolvedSubmission={Boolean(pendingForExam && !receiptRecovered)}
      />
    )
  }

  // The metadata request is read-only. The persisted attempt starts only after
  // the learner explicitly begins from the lobby.
  return (
    <ExamLobby
      examId={examId}
      userId={userId}
      pendingSubmission={pendingForExam}
      refreshResult={() => resultQuery.refetch()}
    />
  )
}

// Rendered inside AppShell via the lobby and runner below.
function ResultShell({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <AppShell title={title} padded>
      {children}
    </AppShell>
  )
}

export function ExamLobby({
  examId,
  userId,
  pendingSubmission,
  refreshResult,
}: {
  examId: string
  userId: string
  pendingSubmission: PendingSubmission | null
  refreshResult: () => Promise<unknown>
}) {
  const queryClient = useQueryClient()
  const [beginRequested, setBeginRequested] = useState(false)
  const [checkingReceipt, setCheckingReceipt] = useState(false)
  const checkedPendingRequestRef = useRef<string | null>(null)
  const infoQuery = useQuery({
    queryKey: ["exam-info", userId, examId],
    queryFn: () => api.examInfo(examId),
    retry: false,
    staleTime: 30_000,
  })
  const examQuery = useQuery({
    queryKey: ["exam", userId, examId],
    queryFn: () => api.getExam(examId),
    enabled: beginRequested,
    retry: false,
    staleTime: 0,
  })

  const closed =
    (infoQuery.isError &&
      infoQuery.error instanceof ApiError &&
      infoQuery.error.code === "EXAM_CLOSED") ||
    (beginRequested &&
      examQuery.isError &&
      examQuery.error instanceof ApiError &&
      examQuery.error.code === "EXAM_CLOSED")

  useEffect(() => {
    if (
      !closed ||
      !pendingSubmission ||
      checkedPendingRequestRef.current === pendingSubmission.requestId
    )
      return
    checkedPendingRequestRef.current = pendingSubmission.requestId
    setCheckingReceipt(true)
    void refreshResult()
      .catch(() => undefined)
      .finally(() => setCheckingReceipt(false))
  }, [closed, pendingSubmission, refreshResult])

  if (examQuery.data) {
    const exam = examQuery.data
    if (exam.questions.length === 0) {
      return (
        <ResultShell title={exam.title}>
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No questions available</EmptyTitle>
              <EmptyDescription>
                Ask your teacher to check this assessment.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        </ResultShell>
      )
    }
    return (
      <ExamRunner
        key={`${userId}:${examId}:${exam.attempt?.attempt_number ?? 1}`}
        userId={userId}
        examId={examId}
        title={exam.title}
        questions={exam.questions}
        attempt={exam.attempt}
        deadlineMs={exam.deadline_ms}
      />
    )
  }

  if (!infoQuery.data && infoQuery.isPending) {
    return (
      <ResultShell title="Quiz">
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
          <Skeleton className="h-10 w-2/3 rounded-lg" />
          <Skeleton className="h-64 w-full rounded-xl" />
        </div>
      </ResultShell>
    )
  }

  if (checkingReceipt) {
    return (
      <ResultShell title="Quiz">
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 pt-10">
          <Alert>
            <CloudUploadIcon />
            <AlertTitle>Checking the saved submission receipt</AlertTitle>
            <AlertDescription>
              The exam window has closed. We’re checking whether the server
              stored the request saved on this device.
            </AlertDescription>
          </Alert>
        </div>
      </ResultShell>
    )
  }

  if (!infoQuery.data) {
    const error = examQuery.error ?? infoQuery.error
    const status = error instanceof ApiError ? error.status : undefined
    const code = error instanceof ApiError ? error.code : undefined
    return (
      <ResultShell title="Quiz">
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 pt-10">
          <Empty className="rounded-xl border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ListChecksIcon />
              </EmptyMedia>
              <EmptyTitle>
                {code === "EXAM_NOT_OPEN"
                  ? "Not open yet"
                  : code === "EXAM_CLOSED"
                    ? "This quiz has closed"
                    : status === 404
                      ? "Quiz not found"
                      : "Quiz unavailable"}
              </EmptyTitle>
              <EmptyDescription>
                {status === 503
                  ? "The platform is failing over right now — try again in a moment."
                  : code === "EXAM_NOT_OPEN"
                    ? "Come back once your teacher opens it."
                    : "It may have been unpublished, or the link is stale."}
              </EmptyDescription>
            </EmptyHeader>
            <Button
              variant="outline"
              nativeButton={false}
              render={<Link to="/" />}
            >
              <ArrowLeftIcon data-icon="inline-start" />
              Back home
            </Button>
          </Empty>
        </div>
      </ResultShell>
    )
  }

  const meta: ExamMeta = infoQuery.data
  const attemptNumber =
    pendingSubmission?.attemptNumber ?? meta.attempt_number ?? 1
  const scope = examScope(examId, attemptNumber, userId)
  const draft = readExamDraft(scope)
  const savedProgress = readQuizProgress(scope, meta.question_count)
  const hasLocalWork = Boolean(
    pendingSubmission ||
    Object.keys(draft).length ||
    savedProgress.position ||
    savedProgress.flaggedIndices.length
  )
  const resumed = meta.attempt_status === "in_progress" || hasLocalWork
  const submitted = meta.attempt_status === "submitted"
  const state = examState(meta)
  const windowOpen = isAssessmentWindowOpen(meta)
  const canBegin = !submitted && (resumed || windowOpen)
  const examStartError =
    beginRequested && examQuery.isError ? examQuery.error : null

  if (submitted) {
    return (
      <ResultShell title={meta.title}>
        <div className="mx-auto w-full max-w-2xl pt-5">
          <Card>
            <CardHeader>
              <CardTitle>Your submission is recorded</CardTitle>
              <CardDescription>
                {typeof meta.score === "number" &&
                typeof meta.total === "number"
                  ? `Your score is ${meta.score} of ${meta.total}.`
                  : "Your result is not available right now."}
              </CardDescription>
            </CardHeader>
            <CardFooter className="gap-2">
              <Button onClick={() => void refreshResult()}>Retry result</Button>
              <Button
                variant="outline"
                nativeButton={false}
                render={<Link to="/" />}
              >
                Back home
              </Button>
            </CardFooter>
          </Card>
        </div>
      </ResultShell>
    )
  }

  return (
    <ResultShell title={meta.title}>
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 pt-5">
        <Card>
          <CardHeader>
            <CardTitle>
              {resumed ? "Continue your assessment" : "Before you begin"}
            </CardTitle>
            <CardDescription>
              {meta.instructions?.trim() ||
                "Review the timing and rules, then begin when you are ready."}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="grid gap-2 sm:grid-cols-2">
              <div className="rounded-lg border p-3">
                <p className="text-xs text-muted-foreground">Questions</p>
                <p className="font-medium">{meta.question_count}</p>
              </div>
              <div className="rounded-lg border p-3">
                <p className="text-xs text-muted-foreground">Time limit</p>
                <p className="font-medium">
                  {meta.duration_minutes
                    ? `${meta.duration_minutes} minutes`
                    : "No time limit"}
                </p>
              </div>
              <div className="rounded-lg border p-3">
                <p className="text-xs text-muted-foreground">Availability</p>
                <p className="font-medium">
                  {state === "open"
                    ? "Open now"
                    : state === "upcoming"
                      ? "Opens later"
                      : windowOpen
                        ? "Accepting late work"
                        : "Closed"}
                </p>
              </div>
              <div className="rounded-lg border p-3">
                <p className="text-xs text-muted-foreground">Attempts</p>
                <p className="font-medium">
                  {(meta.max_attempts ?? 1) > 0
                    ? `${attemptNumber} of ${meta.max_attempts ?? 1}`
                    : `${attemptNumber} · unlimited attempts`}
                </p>
              </div>
            </div>
            {meta.opens_at ? (
              <p className="text-xs text-muted-foreground">
                Opens {new Date(meta.opens_at).toLocaleString()}
              </p>
            ) : null}
            {meta.closes_at ? (
              <p className="text-xs text-muted-foreground">
                Closes {new Date(meta.closes_at).toLocaleString()}
              </p>
            ) : null}
            {meta.allow_late ? (
              <p className="text-xs text-muted-foreground">
                Late submissions are allowed
                {meta.late_grace_minutes
                  ? ` for ${meta.late_grace_minutes} minutes`
                  : ""}
                .
              </p>
            ) : null}
            {hasLocalWork ? (
              <Alert>
                <CloudUploadIcon />
                <AlertTitle>
                  {pendingSubmission
                    ? "A saved submission needs attention"
                    : "Your answers are saved on this device"}
                </AlertTitle>
                <AlertDescription>
                  {pendingSubmission
                    ? "Continue to retry the same saved answer snapshot and request ID."
                    : "Continue this attempt to restore your saved answers and question position."}
                </AlertDescription>
              </Alert>
            ) : null}
            {examStartError ? (
              <Alert variant="destructive">
                <AlertTitle>
                  {examStartError instanceof ApiError &&
                  examStartError.code === "EXAM_CLOSED"
                    ? "This quiz has closed"
                    : "Could not open the assessment"}
                </AlertTitle>
                <AlertDescription>
                  {examStartError instanceof ApiError
                    ? examStartError.message
                    : "Try again when the service is available."}
                </AlertDescription>
              </Alert>
            ) : null}
          </CardContent>
          <CardFooter className="gap-2">
            <Button
              disabled={!canBegin || examQuery.isFetching}
              onClick={() => {
                queryClient.removeQueries({
                  queryKey: ["exam", userId, examId],
                  exact: true,
                })
                setBeginRequested(true)
              }}
            >
              {examQuery.isFetching ? (
                <>
                  <Spinner data-icon="inline-start" />
                  Opening…
                </>
              ) : examStartError ? (
                "Try again"
              ) : resumed ? (
                "Resume assessment"
              ) : (
                "Begin assessment"
              )}
            </Button>
            <Button
              variant="outline"
              nativeButton={false}
              render={<Link to="/" />}
            >
              Back home
            </Button>
          </CardFooter>
        </Card>
      </div>
    </ResultShell>
  )
}

export function ResultView({
  examId,
  userId,
  result,
  meta,
  receiptRecovered = false,
  pendingRequestId,
  attemptNumber,
  unresolvedSubmission = false,
}: {
  examId?: string
  userId: string
  result: ResultResponse
  meta?: ExamMeta
  receiptRecovered?: boolean
  pendingRequestId?: string
  attemptNumber?: number
  unresolvedSubmission?: boolean
}) {
  const queryClient = useQueryClient()
  const [retaking, setRetaking] = useState(false)
  useEffect(() => {
    if (!receiptRecovered || !examId || !pendingRequestId) return
    const scope = examScope(examId, attemptNumber ?? 1, userId)
    clearPendingSubmission(scope, pendingRequestId)
    clearExamDraft(scope)
  }, [attemptNumber, examId, pendingRequestId, receiptRecovered, userId])

  const percent =
    result.total > 0 ? Math.round((result.score / result.total) * 100) : 0
  const receiptText = formatResultReceipt(result)

  async function copyReceipt() {
    try {
      await navigator.clipboard.writeText(receiptText)
      toast.success("Receipt copied")
    } catch {
      toast.error("Could not copy the receipt")
    }
  }

  function downloadReceipt() {
    try {
      const blob = new Blob([receiptText], { type: "text/plain;charset=utf-8" })
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement("a")
      anchor.href = url
      anchor.download = `continuum-receipt-${examId ?? "assessment"}.txt`
      anchor.click()
      URL.revokeObjectURL(url)
    } catch {
      toast.error("Could not download the receipt")
    }
  }

  async function retakeAssessment() {
    if (!examId || !meta || unresolvedSubmission || !canRetakeAssessment(meta))
      return
    setRetaking(true)
    try {
      const attempt = await api.retakeExam(
        examId,
        meta.attempt_number ?? result.attempt_number ?? 1
      )
      queryClient.setQueryData(["exam-info", userId, examId], {
        ...meta,
        attempt_status: "in_progress",
        attempt_number:
          attempt.attempt_number ??
          (meta.attempt_number ?? result.attempt_number ?? 1) + 1,
      })
      await Promise.allSettled([
        queryClient.invalidateQueries({ queryKey: ["result", userId, examId] }),
        queryClient.invalidateQueries({
          queryKey: ["exam-info", userId, examId],
        }),
        queryClient.invalidateQueries({ queryKey: ["exam", userId, examId] }),
      ])
      toast.success("Your next attempt has started. Resume to continue.")
    } catch (error) {
      throw new Error(
        error instanceof ApiError
          ? error.message
          : "Could not start the retake. Retry safely when the service responds."
      )
    } finally {
      setRetaking(false)
    }
  }

  return (
    <ResultShell title="Your result">
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-6 pt-2">
        <Card className="items-center py-10 text-center">
          <CardHeader>
            <CardDescription>Result</CardDescription>
            <CardTitle className="text-5xl font-semibold tracking-tight text-primary">
              {percent}%
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col items-center gap-3">
            {receiptRecovered ? (
              <Badge variant="secondary">
                Receipt recovered for this request
              </Badge>
            ) : pendingRequestId ? (
              <Alert variant="destructive" className="text-left">
                <AlertTitle>Saved request needs review</AlertTitle>
                <AlertDescription>
                  This result belongs to a different request. The locally
                  buffered request remains on this device.
                </AlertDescription>
              </Alert>
            ) : null}
            <p className="text-sm text-muted-foreground">
              You earned{" "}
              <span className="font-medium text-foreground">
                {result.score}
              </span>{" "}
              of{" "}
              <span className="font-medium text-foreground">
                {result.total}
              </span>{" "}
              points.
            </p>
            {result.submitted_at ? (
              <Badge variant="secondary">
                submitted {new Date(result.submitted_at).toLocaleString()}
              </Badge>
            ) : null}
            {result.request_id ? (
              <div className="max-w-full rounded-md border bg-muted/30 px-3 py-2 text-left">
                <p className="text-xs text-muted-foreground">
                  Receipt reference
                </p>
                <code className="block font-mono text-xs break-all">
                  {result.request_id}
                </code>
              </div>
            ) : null}
            <div className="mt-2 flex flex-wrap justify-center gap-2 print:hidden">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void copyReceipt()}
              >
                <CopyIcon data-icon="inline-start" />
                Copy receipt
              </Button>
              <Button variant="outline" size="sm" onClick={downloadReceipt}>
                <DownloadIcon data-icon="inline-start" />
                Download receipt
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => window.print()}
              >
                <PrinterIcon data-icon="inline-start" />
                Print result
              </Button>
              {meta && canRetakeAssessment(meta) && !unresolvedSubmission ? (
                <ConfirmAction
                  title="Start another attempt?"
                  description="Your next timer starts as soon as you confirm. Your previous receipt will remain recorded."
                  confirmLabel="Start timed attempt"
                  onConfirm={retakeAssessment}
                >
                  <Button size="sm" disabled={retaking}>
                    {retaking ? (
                      <>
                        <Spinner data-icon="inline-start" />
                        Starting…
                      </>
                    ) : (
                      "Retake assessment"
                    )}
                  </Button>
                </ConfirmAction>
              ) : null}
              <Button nativeButton={false} render={<Link to="/" />}>
                Back home
              </Button>
            </div>
          </CardContent>
        </Card>

        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-semibold">Answer review</h2>
          {result.review.map((question) => {
            const unanswered = question.chosen === null
            return (
              <Card key={question.index} size="sm">
                <CardContent className="flex flex-col gap-2 pt-3">
                  <div className="flex items-start justify-between gap-3">
                    <p className="text-sm leading-relaxed font-medium">
                      {question.question}
                    </p>
                    {unanswered ? (
                      <Badge variant="secondary">skipped</Badge>
                    ) : question.was_correct ? (
                      <CheckCircle2Icon className="size-4 shrink-0 text-emerald-600" />
                    ) : (
                      <XCircleIcon className="size-4 shrink-0 text-destructive" />
                    )}
                  </div>
                  <div className="flex flex-col gap-1 text-xs">
                    <span className="flex items-center gap-1.5">
                      <span className="text-muted-foreground">
                        Your answer:
                      </span>
                      {unanswered ? (
                        <span className="text-muted-foreground italic">
                          none
                        </span>
                      ) : (
                        <span
                          className={
                            question.was_correct ? "" : "text-destructive"
                          }
                        >
                          {question.options[question.chosen!] ?? "?"}
                        </span>
                      )}
                    </span>
                    {!question.was_correct ? (
                      <span className="flex items-center gap-1.5">
                        <span className="text-muted-foreground">Correct:</span>
                        <span>
                          {question.correct_answer !== null
                            ? question.options[question.correct_answer]
                            : "?"}
                        </span>
                      </span>
                    ) : null}
                    {question.points !== undefined ? (
                      <span className="text-muted-foreground">
                        Points: {question.was_correct ? question.points : 0}/
                        {question.points}
                      </span>
                    ) : null}
                    {question.feedback ? (
                      <p className="mt-1 rounded-md bg-muted/40 p-2 text-sm text-foreground">
                        {question.feedback}
                      </p>
                    ) : null}
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </section>
      </div>
    </ResultShell>
  )
}

export function ExamRunner({
  examId,
  userId,
  title,
  questions,
  attempt,
  deadlineMs,
}: {
  examId: string
  userId: string
  title: string
  questions: { index: number; question: string; options: string[] }[]
  attempt?: AttemptSnapshot
  deadlineMs?: number
}) {
  const queryClient = useQueryClient()
  const scope = useMemo(
    () => examScope(examId, attempt?.attempt_number ?? 1, userId),
    [attempt?.attempt_number, examId, userId]
  )
  const initialPending = useMemo(() => readPendingSubmission(scope), [scope])
  const initialProgress = useMemo(
    () => readQuizProgress(scope, questions.length),
    [questions.length, scope]
  )
  const [position, setPosition] = useState(initialProgress.position)
  const [flaggedIndices, setFlaggedIndices] = useState<number[]>(
    initialProgress.flaggedIndices
  )
  const [questionFilter, setQuestionFilter] = useState<
    "all" | "flagged" | "unanswered"
  >("all")
  const [answers, setAnswers] = useState<Partial<Record<number, number>>>(
    () => ({
      ...(initialPending
        ? initialPending.answers
        : {
            ...attempt?.answers,
            ...loadDraftAnswers(examId, scope.attemptNumber),
          }),
    })
  )
  const [pendingSubmission, setPendingSubmission] =
    useState<PendingSubmission | null>(initialPending)
  const online = useOnlineStatus()
  const serverVersionRef = useRef(attempt?.version ?? 0)
  const startedAt = useMemo(
    () => attempt?.started_at ?? Date.now(),
    [attempt?.started_at]
  )
  const [nowTick, setNowTick] = useState(Date.now())
  const [draftSavedAt, setDraftSavedAt] = useState<number | null>(null)
  const [saveStatus, setSaveStatus] = useState<
    "local" | "saving" | "durable" | "offline" | "error"
  >(attempt?.version ? "durable" : "local")
  const [submissionState, setSubmissionState] = useState<
    | "idle"
    | "buffered"
    | "submitting"
    | "blocked"
    | "confirmed"
    | "receipt-recovered"
    | "storage-error"
  >(
    initialPending?.status === "blocked"
      ? "blocked"
      : initialPending
        ? "buffered"
        : "idle"
  )
  const [retryableFailure, setRetryableFailure] = useState(false)
  const startedRequestRef = useRef<string | null>(null)
  const autoSubmittedRef = useRef(false)

  useEffect(() => {
    const timer = window.setInterval(() => setNowTick(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    writeQuizProgress(scope, { position, flaggedIndices })
  }, [flaggedIndices, position, scope])

  function updateAnswers(next: Partial<Record<number, number>>) {
    if (pendingSubmission) return
    setAnswers(next)
    const saved = writeExamDraft(scope, next)
    setDraftSavedAt(Date.now())
    setSaveStatus(!saved ? "error" : online ? "local" : "offline")
  }

  const saveMutation = useMutation({
    mutationFn: (nextAnswers: Record<string, number>) =>
      api.saveAttempt(examId, {
        answers: nextAnswers,
        expected_version: serverVersionRef.current,
        request_id: crypto.randomUUID(),
        ...(scope.attemptNumber > 1
          ? { attempt_number: scope.attemptNumber }
          : {}),
      }),
    onSuccess: (saved) => {
      serverVersionRef.current = saved.version
      setSaveStatus("durable")
      setDraftSavedAt(Date.now())
    },
    onError: async (error) => {
      if (error instanceof ApiError && error.status === 409) {
        try {
          const latest = await api.getAttempt(examId)
          serverVersionRef.current = latest.version
          setAnswers((current) => ({
            ...latest.answers,
            ...current,
          }))
          setSaveStatus("error")
          toast.error(
            "This quiz changed in another tab. Your local answers were kept for review."
          )
          return
        } catch {
          // Fall through to the generic retryable state.
        }
      }
      setSaveStatus(saveFailureStatus(online))
    },
  })

  function refreshAfterConfirmedReceipt(
    receipt: SubmitResult | ResultResponse
  ) {
    queryClient.setQueryData<ExamMeta>(
      ["exam-info", scope.userId, examId],
      (meta) => {
        if (!meta || (meta.attempt_number ?? 1) > scope.attemptNumber)
          return meta
        return {
          ...meta,
          attempt_status: "submitted",
          attempt_number: scope.attemptNumber,
          score: receipt.score,
          total: receipt.total,
        }
      }
    )
    void Promise.allSettled([
      queryClient.refetchQueries({
        queryKey: ["result", scope.userId, examId],
      }),
      queryClient.refetchQueries({
        queryKey: ["exam-info", scope.userId, examId],
      }),
      queryClient.refetchQueries({ queryKey: ["exams", scope.userId] }),
    ])
  }

  const submitMutation = useMutation({
    mutationFn: async (pending: PendingSubmission) => {
      setPendingSubmissionStatus(scope, pending.requestId, "submitting")
      setSubmissionState("submitting")
      return submitWithRetry(pending, (payload) =>
        api.submitAnswers(examId, payload)
      )
    },
    onSuccess: (receipt: SubmitResult, pending: PendingSubmission) => {
      if (receipt.request_id !== pending.requestId) {
        setPendingSubmissionStatus(scope, pending.requestId, "blocked")
        setSubmissionState("blocked")
        setRetryableFailure(false)
        toast.error(
          "The server returned a receipt for a different request. Your saved request was kept."
        )
        return
      }
      setPendingSubmissionStatus(scope, pending.requestId, "buffered")
      setPendingSubmission((current) =>
        current?.requestId === pending.requestId
          ? { ...current, status: "buffered" }
          : current
      )
      setSubmissionState("confirmed")
      setRetryableFailure(false)
      refreshAfterConfirmedReceipt(receipt)
      toast.success("Submission receipt confirmed")
    },
    onError: async (error: unknown, pending: PendingSubmission) => {
      const retryable = isTransientSubmissionError(error)
      const nextStatus = retryable ? "buffered" : "blocked"
      setRetryableFailure(retryable)
      setPendingSubmissionStatus(scope, pending.requestId, nextStatus)
      setPendingSubmission((current) =>
        current?.requestId === pending.requestId
          ? { ...current, status: nextStatus }
          : current
      )
      setSubmissionState(retryable ? "buffered" : "blocked")

      if (retryable) {
        try {
          const result = await api.myResult(examId)
          if (result.request_id === pending.requestId) {
            setSubmissionState("receipt-recovered")
            setPendingSubmissionStatus(scope, pending.requestId, "buffered")
            queryClient.setQueryData(["result", scope.userId, examId], result)
            refreshAfterConfirmedReceipt(result)
            return
          }
        } catch {
          // No matching result is available, so retain the original payload.
        }
        toast.error(
          "The receipt is still unconfirmed. Your saved request is ready to retry."
        )
      } else {
        toast.error(
          error instanceof ApiError
            ? error.message
            : "Submission stopped; your saved request was kept."
        )
      }
    },
  })

  function startSubmission() {
    let pending: PendingSubmission
    try {
      pending = createOrLoadPendingSubmission(
        scope,
        serialiseAnswerMap(answers)
      )
    } catch (error) {
      setSubmissionState("storage-error")
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not save this submission on the device."
      )
      return
    }
    setPendingSubmission(pending)
    setAnswers(pending.answers)
    setRetryableFailure(false)
    if (!online) {
      setPendingSubmissionStatus(scope, pending.requestId, "buffered")
      setSubmissionState("buffered")
      return
    }
    if (startedRequestRef.current === pending.requestId) return
    startedRequestRef.current = pending.requestId
    submitMutation.mutate(pending)
  }

  useEffect(() => {
    if (!online || !pendingSubmission || pendingSubmission.status === "blocked")
      return
    if (startedRequestRef.current === pendingSubmission.requestId) return
    startedRequestRef.current = pendingSubmission.requestId
    submitMutation.mutate(pendingSubmission)
  }, [online, pendingSubmission])

  useEffect(() => {
    if (
      !online ||
      pendingSubmission ||
      Object.keys(answers).length === 0 ||
      submitMutation.isPending
    )
      return
    const timer = window.setTimeout(() => {
      setSaveStatus("saving")
      saveMutation.mutate(serialiseAnswerMap(answers))
    }, 900)
    return () => window.clearTimeout(timer)
  }, [answers, online, pendingSubmission])

  const remainingMs = deadlineMs !== undefined ? deadlineMs - nowTick : null
  const timeIsUp = remainingMs !== null && remainingMs <= 0

  useEffect(() => {
    if (
      timeIsUp &&
      !autoSubmittedRef.current &&
      !submitMutation.isPending &&
      pendingSubmission?.status !== "blocked"
    ) {
      autoSubmittedRef.current = true
      startSubmission()
      toast.info(
        "Time is up — the current answer snapshot was saved for submission"
      )
    }
  }, [pendingSubmission?.status, timeIsUp, submitMutation.isPending])

  const total = questions.length
  const answeredCount = Object.keys(answers).length
  const current = questions[position]
  const currentAnswer = answers[current.index]
  const isLast = position === total - 1
  const visiblePositions = visibleQuestionPositions(
    total,
    questionFilter,
    flaggedIndices,
    answers
  )
  const minutesUsed = Math.floor((nowTick - startedAt) / 60000)
  const canRetrySameRequest = Boolean(
    pendingSubmission &&
    online &&
    (retryableFailure || submissionState === "buffered")
  )

  useEffect(() => {
    if (visiblePositions.length > 0 && !visiblePositions.includes(position)) {
      setPosition(visiblePositions[0])
    }
  }, [position, visiblePositions])

  function moveToNextQuestion() {
    if (!isLast) setPosition((currentPosition) => currentPosition + 1)
  }

  function clearCurrentAnswer() {
    if (currentAnswer === undefined || pendingSubmission || timeIsUp) return
    const next = { ...answers }
    delete next[current.index]
    updateAnswers(next)
  }

  return (
    <ResultShell title={title}>
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 pt-2">
        {!online ? (
          <Alert variant="destructive">
            <CloudOffIcon />
            <AlertTitle>Offline</AlertTitle>
            <AlertDescription>
              Answers are buffered on this device. Your exam deadline remains
              the one supplied by the server.
            </AlertDescription>
          </Alert>
        ) : null}
        {submissionState !== "idle" ? (
          <Alert
            variant={
              submissionState === "blocked" ||
              submissionState === "storage-error"
                ? "destructive"
                : "default"
            }
          >
            {submissionState === "buffered" ? (
              <CloudOffIcon />
            ) : submissionState === "receipt-recovered" ? (
              <CheckCircle2Icon />
            ) : (
              <SendIcon />
            )}
            <AlertTitle>
              {submissionState === "buffered"
                ? "Submission buffered on this device"
                : submissionState === "submitting"
                  ? "Submitting saved snapshot"
                  : submissionState === "confirmed"
                    ? "Submission receipt confirmed"
                    : submissionState === "receipt-recovered"
                      ? "Receipt recovered"
                      : submissionState === "storage-error"
                        ? "Could not protect this submission"
                        : "Submission stopped"}
            </AlertTitle>
            <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
              <span>
                {submissionState === "buffered"
                  ? "This exact answer snapshot and request ID are saved locally until a receipt is confirmed."
                  : submissionState === "submitting"
                    ? "Retries use the same saved request. A confirmation appears only after a matching receipt."
                    : submissionState === "confirmed"
                      ? "The server returned a receipt for this request. Checking the stored result…"
                      : submissionState === "receipt-recovered"
                        ? "The stored result matches the saved request ID."
                        : submissionState === "storage-error"
                          ? "The request was not sent because it could not be stored safely on this device."
                          : "The saved answer snapshot remains available. Review the error before trying the same request again."}
              </span>
              {pendingSubmission &&
              canRetrySameRequest &&
              submissionState !== "submitting" ? (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    startedRequestRef.current = pendingSubmission.requestId
                    setRetryableFailure(false)
                    submitMutation.mutate(pendingSubmission)
                  }}
                >
                  <RotateCcwIcon data-icon="inline-start" />
                  Retry the same request
                </Button>
              ) : null}
            </AlertDescription>
          </Alert>
        ) : null}
        <Progress
          aria-label="Questions answered"
          value={total > 0 ? (answeredCount / total) * 100 : 0}
        />

        <Card>
          <CardHeader>
            <div className="flex items-start justify-between gap-3">
              <CardTitle className="text-lg leading-relaxed">
                {current.question}
              </CardTitle>
              <Button
                type="button"
                size="sm"
                variant={
                  flaggedIndices.includes(current.index)
                    ? "secondary"
                    : "outline"
                }
                disabled={Boolean(pendingSubmission) || timeIsUp}
                aria-pressed={flaggedIndices.includes(current.index)}
                onClick={() =>
                  setFlaggedIndices((indices) =>
                    toggleFlaggedQuestion(indices, current.index)
                  )
                }
              >
                <FlagIcon data-icon="inline-start" />
                {flaggedIndices.includes(current.index)
                  ? "Flagged for review"
                  : "Flag for review"}
              </Button>
            </div>
          </CardHeader>
          <CardContent>
            <RadioGroup
              value={currentAnswer !== undefined ? String(currentAnswer) : null}
              disabled={Boolean(pendingSubmission) || timeIsUp}
              onValueChange={(value) => {
                applyRadioChoice(value, (choice) => {
                  updateAnswers({
                    ...answers,
                    [current.index]: choice,
                  })
                })
              }}
              className="gap-2"
            >
              {current.options.map((option, optionIndex) => (
                <label
                  key={`${current.index}-${optionIndex}`}
                  className="flex cursor-pointer items-center gap-3 rounded-lg border p-3 text-sm transition-colors hover:bg-muted/60 has-data-checked:border-primary has-data-checked:bg-primary/5"
                >
                  <RadioGroupItem
                    value={String(optionIndex)}
                    disabled={Boolean(pendingSubmission) || timeIsUp}
                  />
                  <span>{option}</span>
                  {currentAnswer === optionIndex ? (
                    <CheckIcon className="ml-auto size-4 text-primary" />
                  ) : null}
                </label>
              ))}
            </RadioGroup>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="mt-3"
              disabled={
                currentAnswer === undefined ||
                Boolean(pendingSubmission) ||
                timeIsUp
              }
              onClick={clearCurrentAnswer}
            >
              Clear answer
            </Button>
          </CardContent>
          <CardFooter className="justify-between gap-2">
            <Button
              variant="ghost"
              disabled={position === 0 || timeIsUp}
              onClick={() =>
                setPosition((currentPosition) => currentPosition - 1)
              }
            >
              <ArrowLeftIcon data-icon="inline-start" />
              Previous
            </Button>
            {isLast ? (
              <AlertDialog>
                <AlertDialogTrigger
                  render={
                    <Button
                      disabled={
                        answeredCount === 0 ||
                        submitMutation.isPending ||
                        timeIsUp ||
                        Boolean(pendingSubmission)
                      }
                    >
                      {submitMutation.isPending ? (
                        <>
                          <Spinner data-icon="inline-start" />
                          Submitting…
                        </>
                      ) : (
                        <>
                          <SendIcon data-icon="inline-start" />
                          Submit quiz
                        </>
                      )}
                    </Button>
                  }
                />
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>Submit your answers?</AlertDialogTitle>
                    <AlertDialogDescription>
                      {answeredCount === total
                        ? "All questions are answered. This can't be undone."
                        : `${total - answeredCount} question${total - answeredCount === 1 ? "" : "s"} unanswered. Unanswered questions score zero.`}
                      {online
                        ? " A request ID and answer snapshot will be saved before the server call."
                        : " This exact snapshot will stay buffered on this device until you reconnect."}
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>Keep editing</AlertDialogCancel>
                    <AlertDialogAction
                      onClick={startSubmission}
                      disabled={submitMutation.isPending}
                    >
                      {online ? "Save and submit" : "Buffer submission"}
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            ) : (
              <Button disabled={timeIsUp} onClick={moveToNextQuestion}>
                {currentAnswer === undefined ? "Skip question" : "Next"}
                <ArrowRightIcon data-icon="inline-end" />
              </Button>
            )}
          </CardFooter>
        </Card>

        <div className="flex flex-col gap-2 rounded-lg border p-3">
          <div className="flex items-center justify-between gap-3">
            <p className="text-sm font-medium">Question navigator</p>
            <Select
              value={questionFilter}
              onValueChange={(value) => setQuestionFilter(value || "all")}
            >
              <SelectTrigger aria-label="Filter questions" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All questions</SelectItem>
                <SelectItem value="flagged">Flagged</SelectItem>
                <SelectItem value="unanswered">Unanswered</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div
            className="flex flex-wrap items-center gap-1.5"
            aria-label="Question list"
          >
            {visiblePositions.map((index) => {
              const question = questions[index]
              const isFlagged = flaggedIndices.includes(question.index)
              const isAnswered = answers[question.index] !== undefined
              return (
                <Button
                  key={question.index}
                  type="button"
                  size="icon-sm"
                  variant={
                    index === position
                      ? "default"
                      : isAnswered
                        ? "secondary"
                        : "outline"
                  }
                  onClick={() => setPosition(index)}
                  aria-label={`Go to question ${index + 1}${isFlagged ? ", flagged for review" : ""}${isAnswered ? ", answered" : ", unanswered"}`}
                >
                  {isFlagged ? <FlagIcon data-icon="inline-start" /> : null}
                  {index + 1}
                </Button>
              )
            })}
            {visiblePositions.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No questions match this filter.
              </p>
            ) : null}
          </div>
        </div>

        <p className="sr-only" role="status" aria-live="polite">
          {remainingMs !== null
            ? timeIsUp
              ? "Time is up."
              : `${Math.ceil(remainingMs / 1000)} seconds remaining`
            : ""}
        </p>

        <div className="flex flex-wrap items-center justify-center gap-2 pb-2 text-xs text-muted-foreground">
          <ClockIcon className="size-3.5" />
          {remainingMs !== null ? (
            <Badge
              variant={remainingMs < 60_000 ? "destructive" : "outline"}
              aria-label="Time remaining"
            >
              {clockLabel(remainingMs)} left
            </Badge>
          ) : (
            <Badge variant="outline" aria-label="Elapsed time">
              {minutesUsed} min
            </Badge>
          )}
          <Badge variant="secondary">
            {answeredCount}/{total} answered
          </Badge>
          <Badge
            variant={
              !online || saveStatus === "error" ? "destructive" : "outline"
            }
          >
            {online ? (
              <CloudUploadIcon data-icon="inline-start" />
            ) : (
              <CloudOffIcon data-icon="inline-start" />
            )}
            {online
              ? saveStatus === "saving"
                ? "Saving…"
                : saveStatus === "durable"
                  ? "Durably saved"
                  : saveStatus === "error"
                    ? "Save needs retry"
                    : "Saved locally"
              : "Offline draft"}
          </Badge>
          <span>
            {draftSavedAt
              ? `Saved locally ${new Date(draftSavedAt).toLocaleTimeString()}`
              : "Answers save locally as you go."}
          </span>
        </div>
      </div>
    </ResultShell>
  )
}
