import { useEffect, useMemo, useRef, useState } from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  CheckIcon,
  ClockIcon,
  ListChecksIcon,
  SendIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import type { SubmitResult } from "@/lib/types"
import { requireAuth } from "@/lib/guard"
import { useMounted } from "@/hooks/use-mounted"
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

function elapsedLabel(startedAt: number): string {
  const seconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000))
  const minutes = Math.floor(seconds / 60)
  return `${String(minutes).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`
}

function draftKey(examId: string): string {
  return `slate.quiz.${examId}`
}

function loadDraftAnswers(examId: string): Partial<Record<number, number>> {
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

function saveDraftAnswers(examId: string, answers: Partial<Record<number, number>>): void {
  try {
    window.localStorage.setItem(draftKey(examId), JSON.stringify(answers))
  } catch {
    // storage unavailable — drafts stay in memory only
  }
}

function clearDraftAnswers(examId: string): void {
  try {
    window.localStorage.removeItem(draftKey(examId))
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

  const examQuery = useQuery({
    queryKey: ["exam", examId],
    queryFn: () => api.getExam(examId),
    enabled: mounted,
    retry: false,
  })

  if (!mounted || examQuery.isPending) {
    return (
      <AppShell title="Quiz" padded>
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
          <Skeleton className="h-10 w-2/3 rounded-lg" />
          <Skeleton className="h-64 w-full rounded-xl" />
        </div>
      </AppShell>
    )
  }

  if (examQuery.isError) {
    const status = examQuery.error instanceof ApiError ? examQuery.error.status : undefined
    return (
      <AppShell title="Quiz">
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 pt-10">
          <Empty className="rounded-xl border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ListChecksIcon />
              </EmptyMedia>
              <EmptyTitle>
                {status === 404 ? "Quiz not found" : "Quiz unavailable"}
              </EmptyTitle>
              <EmptyDescription>
                {status === 503
                  ? "The platform is failing over right now — try again in a moment."
                  : "It may have been unpublished, or the link is stale."}
              </EmptyDescription>
            </EmptyHeader>
            <Button variant="outline" render={<Link to="/" />}>
              <ArrowLeftIcon data-icon="inline-start" />
              Back home
            </Button>
          </Empty>
        </div>
      </AppShell>
    )
  }

  return (
    <ExamRunner
      key={examQuery.data.id}
      examId={examQuery.data.id}
      title={examQuery.data.title}
      questions={examQuery.data.questions}
    />
  )
}

function ExamRunner({
  examId,
  title,
  questions,
}: {
  examId: string
  title: string
  questions: { index: number; question: string; options: string[] }[]
}) {
  const [position, setPosition] = useState(0)
  const [answers, setAnswers] = useState<Partial<Record<number, number>>>(() =>
    loadDraftAnswers(examId),
  )
  const [result, setResult] = useState<SubmitResult | null>(null)
  const [alreadySubmitted, setAlreadySubmitted] = useState(false)
  const requestIdRef = useRef<string | null>(loadRequestId(examId))
  const startedAt = useMemo(() => Date.now(), [])
  const [, forceTick] = useState(0)

  useEffect(() => {
    const timer = window.setInterval(() => forceTick((tick) => tick + 1), 1000)
    return () => window.clearInterval(timer)
  }, [])

  function updateAnswers(next: Partial<Record<number, number>>) {
    setAnswers(next)
    saveDraftAnswers(examId, next)
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

  const submitMutation = useMutation({
    mutationFn: async () => {
      const serialised: Record<string, number> = {}
      for (const [index, choice] of Object.entries(answers)) {
        if (typeof choice === "number") serialised[index] = choice
      }
      return api.submitAnswers(examId, {
        answers: serialised,
        request_id: currentRequestId(),
      })
    },
    onSuccess: (submitResult) => {
      setResult(submitResult)
      clearDraftAnswers(examId)
      toast.success("Answers submitted")
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        setAlreadySubmitted(true)
        clearDraftAnswers(examId)
        return
      }
      toast.error(error instanceof ApiError ? error.message : "Submission failed")
    },
  })

  const total = questions.length
  const answeredCount = Object.keys(answers).length
  const current = questions[position]
  const currentAnswer = answers[current.index]
  const isLast = position === total - 1

  if (alreadySubmitted) {
    return (
      <AppShell title={title}>
        <div className="mx-auto flex w-full max-w-xl flex-col gap-6 pt-6">
          <Card className="items-center py-10 text-center">
            <CardHeader>
              <CardDescription>Already submitted</CardDescription>
              <CardTitle className="text-2xl font-semibold tracking-tight">
                One attempt per quiz
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col items-center gap-3">
              <p className="text-sm text-muted-foreground">
                This browser already submitted answers for this quiz. Ask your teacher to review
                your result.
              </p>
              <div className="mt-2 flex gap-2">
                <Button render={<Link to="/" />}>Back home</Button>
              </div>
            </CardContent>
          </Card>
        </div>
      </AppShell>
    )
  }

  if (result) {
    const percent = result.total > 0 ? Math.round((result.score / result.total) * 100) : 0
    return (
      <AppShell title={title}>
        <div className="mx-auto flex w-full max-w-xl flex-col gap-6 pt-6">
          <Card className="items-center py-10 text-center">
            <CardHeader>
              <CardDescription>Result</CardDescription>
              <CardTitle className="text-5xl font-semibold tracking-tight text-primary">
                {percent}%
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col items-center gap-3">
              <p className="text-sm text-muted-foreground">
                You answered <span className="font-medium text-foreground">{result.score}</span>{" "}
                of <span className="font-medium text-foreground">{result.total}</span> correctly.
              </p>
              {result.status === "duplicate" ? (
                <Badge variant="secondary">already submitted — showing first attempt</Badge>
              ) : null}
              <div className="mt-2 flex gap-2">
                <Button render={<Link to="/" />}>Back home</Button>
              </div>
            </CardContent>
          </Card>
        </div>
      </AppShell>
    )
  }

  return (
    <AppShell
      title={title}
      description={`Question ${position + 1} of ${total}`}
      actions={
        <>
          <ClockIcon data-icon="inline-start" className="hidden sm:block" />
          <Badge variant="outline">{elapsedLabel(startedAt)}</Badge>
          <Badge variant="secondary">
            {answeredCount}/{total} answered
          </Badge>
        </>
      }
    >
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
        <Progress value={(answeredCount / total) * 100} />

        <Card>
          <CardHeader>
            <CardTitle className="text-lg leading-relaxed">{current.question}</CardTitle>
          </CardHeader>
          <CardContent>
            <RadioGroup
              value={currentAnswer !== undefined ? String(currentAnswer) : null}
              onValueChange={(value) => {
                if (typeof value === "string") {
                  updateAnswers({
                    ...answers,
                    [current.index]: Number(value),
                  })
                }
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
              disabled={position === 0}
              onClick={() => setPosition((currentPosition) => currentPosition - 1)}
            >
              <ArrowLeftIcon data-icon="inline-start" />
              Previous
            </Button>
            {isLast ? (
              <AlertDialog>
                <AlertDialogTrigger
                  render={
                    <Button disabled={answeredCount === 0 || submitMutation.isPending}>
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
                disabled={currentAnswer === undefined}
                onClick={() => setPosition((currentPosition) => currentPosition + 1)}
              >
                Next
                <ArrowRightIcon data-icon="inline-end" />
              </Button>
            )}
          </CardFooter>
        </Card>

        <div className="flex flex-wrap justify-center gap-1.5">
          {questions.map((question, index) => (
            <button
              key={question.index}
              onClick={() => setPosition(index)}
              aria-label={`Go to question ${index + 1}`}
              className={
                index === position
                  ? "size-7 rounded-md bg-primary text-xs font-medium text-primary-foreground"
                  : answers[question.index] !== undefined
                    ? "size-7 rounded-md border border-primary/40 bg-primary/10 text-xs font-medium"
                    : "size-7 rounded-md border text-xs font-medium text-muted-foreground hover:bg-muted"
              }
            >
              {index + 1}
            </button>
          ))}
        </div>
      </div>
    </AppShell>
  )
}
