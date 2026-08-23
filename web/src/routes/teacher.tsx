import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  EyeIcon,
  KeyRoundIcon,
  PlusIcon,
  RefreshCwIcon,
  SendIcon,
  Trash2Icon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { useAdminToken } from "@/lib/admin-token"
import type { QuestionDraft } from "@/lib/types"
import { useMounted } from "@/hooks/use-mounted"
import { AppShell } from "@/components/layout/app-shell"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
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
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"

export const Route = createFileRoute("/teacher")({ component: TeacherPage })

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

function TeacherPage() {
  const [tab, setTab] = useState("create")

  return (
    <AppShell
      title="Teacher studio"
      description="Author quizzes and review submissions"
      actions={
        <Tabs value={tab} onValueChange={(value) => setTab(value)}>
          <TabsList>
            <TabsTrigger value="create">Create</TabsTrigger>
            <TabsTrigger value="submissions">Submissions</TabsTrigger>
          </TabsList>
        </Tabs>
      }
    >
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-6">
        <Tabs value={tab} onValueChange={(value) => setTab(value)}>
          <TabsContent value="create">
            <QuizBuilder onPublished={() => setTab("submissions")} />
          </TabsContent>
          <TabsContent value="submissions">
            <SubmissionsPanel />
          </TabsContent>
        </Tabs>
      </div>
    </AppShell>
  )
}

function QuizBuilder({ onPublished }: { onPublished: () => void }) {
  const queryClient = useQueryClient()
  const [adminToken, saveAdminToken] = useAdminToken()
  const [title, setTitle] = useState("")
  const [customId, setCustomId] = useState("")
  const [questions, setQuestions] = useState<BuilderQuestion[]>(() => [blankQuestion()])

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

  const publishMutation = useMutation({
    mutationFn: async () => {
      const draft = {
        id: customId.trim() || undefined,
        title: title.trim(),
        questions: questions.map(({ question, options, answer }) => ({
          question: question.trim(),
          options: options.map((option) => option.trim()),
          answer,
        })),
      }
      return api.createExam(draft, adminToken)
    },
    onSuccess: (created) => {
      toast.success(`Published "${created.title}"`, {
        description: `${created.question_count} questions · id ${created.id}`,
      })
      void queryClient.invalidateQueries({ queryKey: ["exams"] })
      setTitle("")
      setCustomId("")
      setQuestions([blankQuestion()])
      onPublished()
    },
    onError: (error) => {
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
            <Field>
              <FieldLabel htmlFor="admin-token">Admin token</FieldLabel>
              <InputGroup>
                <InputGroupInput
                  id="admin-token"
                  type="password"
                  placeholder="continuum-demo-token"
                  value={adminToken}
                  onChange={(event) => saveAdminToken(event.target.value)}
                />
              </InputGroup>
              <FieldDescription>Saved in this browser only.</FieldDescription>
            </Field>
          </FieldGroup>
        </CardContent>
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
          </CardContent>
        </Card>
      ))}

      <div className="flex items-center justify-between gap-3">
        <Button variant="outline" onClick={() => setQuestions((current) => [...current, blankQuestion()])}>
          <PlusIcon data-icon="inline-start" />
          Add question
        </Button>
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
    </div>
  )
}

function SubmissionsPanel() {
  const mounted = useMounted()
  const [adminToken] = useAdminToken()

  const submissionsQuery = useQuery({
    queryKey: ["submissions", adminToken],
    queryFn: () => api.submissions(adminToken),
    enabled: mounted && adminToken.length > 0,
    refetchInterval: 10_000,
  })

  if (!adminToken) {
    return (
      <Empty className="rounded-xl border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <KeyRoundIcon />
          </EmptyMedia>
          <EmptyTitle>Admin token required</EmptyTitle>
          <EmptyDescription>
            Paste your admin token on the Create tab to load submissions.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
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
