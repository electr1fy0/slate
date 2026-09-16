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
  ListChecksIcon,
  SendIcon,
  XCircleIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import type { AttemptSnapshot, ResultResponse } from "@/lib/types"
import { requireAuth } from "@/lib/guard"
import { useMounted } from "@/hooks/use-mounted"
import { useOnlineStatus } from "@/hooks/use-online-status"
import { AppShell } from "@/components/layout/app-shell"
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
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Progress } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"

export const Route = createFileRoute("/quiz/$examId")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: QuizPage,
  head: () => ({ meta: [{ title: "Quiz · Slate" }] }),
})

function clockLabel(msRemaining: number): string {
  const seconds = Math.max(0, Math.ceil(msRemaining / 1000))
  const minutes = Math.floor(seconds / 60)
  return `${String(minutes).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`
}

function draftKey(examId: string): string {
  return `slate.quiz.${examId}`
}

export function loadDraftAnswers(examId: string): Partial<Record<number, number>> {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(draftKey(examId)) ?? "{}")
    if (typeof parsed !== "object" || parsed === null) return {}
    const answers: Partial<Record<number, number>> = {}
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      const index = Number(key)
      if (Number.isInteger(index) && typeof value === "number") answers[index] = value
    }
    return answers
  } catch {
    return {}
  }
}

export function serialiseAnswerMap(answers: Partial<Record<number, number>>): Record<string, number> {
  const serialised: Record<string, number> = {}
  for (const [index, choice] of Object.entries(answers)) {
    if (typeof choice === "number") serialised[index] = choice
  }
  return serialised
}

export function saveFailureStatus(online: boolean): "error" | "offline" {
  return online ? "error" : "offline"
}

export function applyRadioChoice(value: unknown, onChoice: (choice: number) => void): void {
  if (typeof value === "string") onChoice(Number(value))
}

function saveDraftAnswers(examId: string, answers: Partial<Record<number, number>>): void {
  try {
    window.localStorage.setItem(draftKey(examId), JSON.stringify(answers))
  } catch {
    // storage unavailable — drafts stay in memory only
  }
}

function clearDrafts(examId: string): void {
  try {
    window.localStorage.removeItem(draftKey(examId))
    window.localStorage.removeItem(`${draftKey(examId)}.request-id`)
  } catch {
    // ignore
  }
}

function loadRequestId(examId: string): string | null {
  try {
    return window.localStorage.getItem(`${draftKey(examId)}.request-id`)
  } catch {
    return null
  }
}

function saveRequestId(examId: string, requestId: string): void {
  try {
    window.localStorage.setItem(`${draftKey(examId)}.request-id`, requestId)
  } catch {
    // ignore
  }
}

function QuizPage() {
  const { examId } = Route.useParams()
  const mounted = useMounted()

  const resultQuery = useQuery({
    queryKey: ["result", examId],
    queryFn: () => api.myResult(examId),
    enabled: mounted,
    retry: false,
    staleTime: Infinity,
  })

  if (!mounted || resultQuery.isPending) {
    return (
      <AppShell title="Quiz" padded>
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
          <Skeleton className="h-10 w-2/3 rounded-lg" />
          <Skeleton className="h-64 w-full rounded-xl" />
        </div>
      </AppShell>
    )
  }

  if (resultQuery.data) {
    return <ResultView result={resultQuery.data} />
  }

  // No stored submission (404 or transient failure) — run the quiz.
  return <RunnerGate examId={examId} />
}

// Rendered inside AppShell via RunnerGate/ResultShell wrappers below.
function ResultShell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <AppShell title={title} padded>
      {children}
    </AppShell>
  )
}

function ResultView({ result }: { result: ResultResponse }) {
  const percent = result.total > 0 ? Math.round((result.score / result.total) * 100) : 0
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
            <p className="text-sm text-muted-foreground">
              You answered <span className="font-medium text-foreground">{result.score}</span> of{" "}
              <span className="font-medium text-foreground">{result.total}</span> correctly.
            </p>
            {result.submitted_at ? (
              <Badge variant="secondary">
                submitted {new Date(result.submitted_at).toLocaleString()}
              </Badge>
            ) : null}
            <div className="mt-2 flex gap-2">
              <Button nativeButton={false} render={<Link to="/" />}>Back home</Button>
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
                    <p className="text-sm leading-relaxed font-medium">{question.question}</p>
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
                      <span className="text-muted-foreground">Your answer:</span>
                      {unanswered ? (
                        <span className="italic text-muted-foreground">none</span>
                      ) : (
                        <span className={question.was_correct ? "" : "text-destructive"}>
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

function RunnerGate({ examId }: { examId: string }) {
  const examQuery = useQuery({
    queryKey: ["exam", examId],
    queryFn: () => api.getExam(examId),
    retry: false,
    staleTime: Infinity,
  })

  if (examQuery.isPending) {
    return (
      <ResultShell title="Quiz">
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
          <Skeleton className="h-10 w-2/3 rounded-lg" />
          <Skeleton className="h-64 w-full rounded-xl" />
        </div>
      </ResultShell>
    )
  }

  if (examQuery.isError) {
    const error = examQuery.error
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
            <Button variant="outline" nativeButton={false} render={<Link to="/" />}>
              <ArrowLeftIcon data-icon="inline-start" />
              Back home
            </Button>
          </Empty>
        </div>
      </ResultShell>
    )
  }

  const exam = examQuery.data
  return (
    <ExamRunner
      // Keep submission identity tied to the URL that was authenticated and
      // fetched. Older API responses omitted `id`, which produced
      // /exam/undefined/submit even though the exam loaded successfully.
      key={examId}
      examId={examId}
      title={exam.title}
      questions={exam.questions}
      attempt={exam.attempt}
      deadlineMs={exam.deadline_ms}
    />
  )
}

function ExamRunner({
  examId,
  title,
  questions,
  attempt,
  deadlineMs,
}: {
  examId: string
  title: string
  questions: { index: number; question: string; options: string[] }[]
  attempt?: AttemptSnapshot
  deadlineMs?: number
}) {
  const queryClient = useQueryClient()
  const [position, setPosition] = useState(0)
  const [answers, setAnswers] = useState<Partial<Record<number, number>>>(() => ({
    ...attempt?.answers,
    ...loadDraftAnswers(examId),
  }))
  const online = useOnlineStatus()
  const requestIdRef = useRef<string | null>(loadRequestId(examId))
  const saveRequestIdRef = useRef<string | null>(null)
  const serverVersionRef = useRef(attempt?.version ?? 0)
  const startedAt = useMemo(() => attempt?.started_at ?? Date.now(), [attempt?.started_at])
  const [nowTick, setNowTick] = useState(Date.now())
  const [draftSavedAt, setDraftSavedAt] = useState<number | null>(null)
  const [saveStatus, setSaveStatus] = useState<"saved" | "pending" | "offline" | "error">(
    attempt?.version ? "saved" : "pending",
  )
  const autoSubmittedRef = useRef(false)

  useEffect(() => {
    const timer = window.setInterval(() => setNowTick(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])

  function updateAnswers(next: Partial<Record<number, number>>) {
    setAnswers(next)
    saveDraftAnswers(examId, next)
    setDraftSavedAt(Date.now())
    setSaveStatus(online ? "pending" : "offline")
  }

  function nextSaveRequestId(): string {
    const id = crypto.randomUUID()
    saveRequestIdRef.current = id
    return id
  }

  function currentRequestId(): string {
    let requestId = requestIdRef.current
    if (!requestId) {
      requestId = crypto.randomUUID()
      requestIdRef.current = requestId
      saveRequestId(examId, requestId)
    }
    return requestId
  }

  const saveMutation = useMutation({
    mutationFn: (nextAnswers: Record<string, number>) => api.saveAttempt(examId, {
      answers: nextAnswers,
      expected_version: serverVersionRef.current,
      request_id: nextSaveRequestId(),
    }),
    onSuccess: (saved) => {
      serverVersionRef.current = saved.version
      setSaveStatus("saved")
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
          toast.error("This quiz changed in another tab. Your local answers were kept for review.")
          return
        } catch {
          // Fall through to the generic retryable state.
        }
      }
      setSaveStatus(saveFailureStatus(online))
    },
  })

  useEffect(() => {
    if (!online || Object.keys(answers).length === 0 || submitMutation.isPending) return
    const timer = window.setTimeout(() => {
      saveMutation.mutate(serialiseAnswerMap(answers))
    }, 900)
    return () => window.clearTimeout(timer)
  }, [answers, online])

  const submitMutation = useMutation({
    mutationFn: async () => {
      const requestId = currentRequestId()
      const currentAnswers = serialiseAnswerMap(answers)
      if (online) {
        const saved = await api.saveAttempt(examId, {
          answers: currentAnswers,
          expected_version: serverVersionRef.current,
          request_id: nextSaveRequestId(),
        })
        serverVersionRef.current = saved.version
        return api.submitSavedAttempt(examId, requestId)
      }
      return api.submitAnswers(examId, { answers: currentAnswers, request_id: requestId })
    },
    onSuccess: () => {
      setSaveStatus("saved")
      clearDrafts(examId)
      void queryClient.refetchQueries({ queryKey: ["result", examId] })
      toast.success("Answers submitted")
    },
    onError: (error) => {
      if (error instanceof ApiError &&
          (error.code === "ATTEMPT_EXPIRED" || error.code === "EXAM_CLOSED")) {
        toast.error(error.message)
        return
      }
      if (error instanceof ApiError && error.status === 409) {
        // Already submitted from somewhere else — show the stored result.
        clearDrafts(examId)
        void queryClient.refetchQueries({ queryKey: ["result", examId] })
        return
      }
      toast.error(error instanceof ApiError ? error.message : "Submission failed")
    },
  })

  const remainingMs = deadlineMs !== undefined ? deadlineMs - nowTick : null
  const timeIsUp = remainingMs !== null && remainingMs <= 0

  useEffect(() => {
    if (timeIsUp && !autoSubmittedRef.current && !submitMutation.isPending) {
      autoSubmittedRef.current = true
      submitMutation.mutate()
      toast.info("Time is up — your answers were submitted automatically")
    }
  }, [timeIsUp, submitMutation])

  const total = questions.length
  const answeredCount = Object.keys(answers).length
  const current = questions[position]
  const currentAnswer = answers[current.index]
  const isLast = position === total - 1
  const minutesUsed = Math.floor((nowTick - startedAt) / 60000)

  return (
    <ResultShell title={title}>
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 pt-2">
        {!online ? (
          <Alert variant="destructive">
            <CloudOffIcon />
            <AlertDescription>
              You’re offline. Your answers are saved on this device and will remain here until you
              reconnect. Do not close this tab before submitting.
            </AlertDescription>
          </Alert>
        ) : null}
        <Progress value={(answeredCount / total) * 100} />

        <Card>
          <CardHeader>
            <CardTitle className="text-lg leading-relaxed">{current.question}</CardTitle>
          </CardHeader>
          <CardContent>
            <RadioGroup
              value={currentAnswer !== undefined ? String(currentAnswer) : null}
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
                  <RadioGroupItem value={String(optionIndex)} />
                  <span>{option}</span>
                  {currentAnswer === optionIndex ? (
                    <CheckIcon className="ml-auto size-4 text-primary" />
                  ) : null}
                </label>
              ))}
            </RadioGroup>
          </CardContent>
          <CardFooter className="justify-between gap-2">
            <Button
              variant="ghost"
              disabled={position === 0 || timeIsUp}
              onClick={() => setPosition((currentPosition) => currentPosition - 1)}
            >
              <ArrowLeftIcon data-icon="inline-start" />
              Previous
            </Button>
            {isLast ? (
              <AlertDialog>
                <AlertDialogTrigger
                  render={
                    <Button disabled={answeredCount === 0 || submitMutation.isPending || timeIsUp}>
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
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>Keep editing</AlertDialogCancel>
                    <AlertDialogAction
                      onClick={() => submitMutation.mutate()}
                      disabled={submitMutation.isPending}
                    >
                      Submit
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            ) : (
              <Button
                disabled={currentAnswer === undefined || timeIsUp}
                onClick={() => setPosition((currentPosition) => currentPosition + 1)}
              >
                Next
                <ArrowRightIcon data-icon="inline-end" />
              </Button>
            )}
          </CardFooter>
        </Card>

        <div className="flex flex-wrap items-center justify-center gap-1.5" aria-label="Question navigator">
          {questions.map((question, index) => (
            <Button
              key={question.index}
              type="button"
              size="icon-sm"
              variant={index === position ? "default" : answers[question.index] !== undefined ? "secondary" : "outline"}
              onClick={() => setPosition(index)}
              aria-label={`Go to question ${index + 1}`}
            >
              {index + 1}
            </Button>
          ))}
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
          <Badge variant="secondary">{answeredCount}/{total} answered</Badge>
          <Badge variant={!online || saveStatus === "error" ? "destructive" : "outline"}>
            {online ? <CloudUploadIcon data-icon="inline-start" /> : <CloudOffIcon data-icon="inline-start" />}
            {online
              ? saveStatus === "pending"
                ? "Saving…"
                : saveStatus === "error"
                  ? "Save needs retry"
                  : "Saved to cloud"
              : "Offline draft"}
          </Badge>
          <span>
            {draftSavedAt ? `Saved locally ${new Date(draftSavedAt).toLocaleTimeString()}` : "Answers save locally as you go."}
          </span>
        </div>
      </div>
    </ResultShell>
  )
}
