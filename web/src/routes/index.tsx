import { useState } from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowRightIcon,
  ClipboardListIcon,
  PlusIcon,
  PresentationIcon,
  SparklesIcon,
  UserIcon,
} from "lucide-react"

import { api } from "@/lib/api"
import { newRoomCode, normaliseRoomCode } from "@/lib/room-code"
import { useSession } from "@/lib/session"
import { useMounted } from "@/hooks/use-mounted"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

export const Route = createFileRoute("/")({
  component: HomePage,
  head: () => ({ meta: [{ title: "Slate — Classroom boards & quizzes" }] }),
})

function HomePage() {
  const navigate = useNavigate()
  const mounted = useMounted()
  const [session, setSession] = useSession()
  const [joinCode, setJoinCode] = useState("")

  const examsQuery = useQuery({
    queryKey: ["exams"],
    queryFn: api.listExams,
    enabled: mounted,
  })

  function joinBoard(code: string) {
    const clean = normaliseRoomCode(code)
    if (!clean) {
      toast.error("Enter a room code first")
      return
    }
    void navigate({ to: "/board/$roomId", params: { roomId: clean } })
  }

  return (
    <AppShell title="Home" description="Your classroom at a glance">
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-6">
        <section className="flex flex-col gap-1.5">
          <h2 className="text-2xl font-semibold tracking-tight md:text-3xl">
            {session.role === "teacher" ? "Run today's class" : "Join today's class"}
          </h2>
          <p className="max-w-prose text-sm text-muted-foreground">
            {session.role === "teacher"
              ? "Spin up a live board, publish a quiz, and keep every student on the same page."
              : "Hop onto the live board or take the quiz your teacher published."}
          </p>
        </section>

        <div className="grid gap-4 lg:grid-cols-3">
          <Card className="lg:col-span-2">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <PresentationIcon data-icon="inline-start" />
                Live board
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
            <CardFooter className="gap-2">
              <Button
                variant="secondary"
                onClick={() => {
                  const code = newRoomCode()
                  setJoinCode(code)
                  void navigate({ to: "/board/$roomId", params: { roomId: code } })
                }}
              >
                <PlusIcon data-icon="inline-start" />
                New room
              </Button>
              {session.role === "teacher" ? (
                <Button render={<Link to="/teacher" />}>
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
              <CardDescription>How your classmates see you.</CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Field>
                  <FieldLabel>Role</FieldLabel>
                  <ToggleGroup
                    variant="outline"
                    value={[session.role]}
                    onValueChange={(groupValue) => {
                      const role = groupValue[0]
                      if (role === "teacher" || role === "student") setSession({ role })
                    }}
                    className="w-full"
                  >
                    <ToggleGroupItem value="teacher" className="flex-1">
                      Teacher
                    </ToggleGroupItem>
                    <ToggleGroupItem value="student" className="flex-1">
                      Student
                    </ToggleGroupItem>
                  </ToggleGroup>
                </Field>
                <Field>
                  <FieldLabel htmlFor="display-name">Display name</FieldLabel>
                  <InputGroup>
                    <InputGroupInput
                      id="display-name"
                      placeholder="Ada Lovelace"
                      value={session.name}
                      onChange={(event) => setSession({ name: event.target.value })}
                    />
                  </InputGroup>
                </Field>
              </FieldGroup>
            </CardContent>
          </Card>
        </div>

        <section className="flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">Published quizzes</h3>
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
                <EmptyTitle>Couldn't load quizzes</EmptyTitle>
                <EmptyDescription>
                  Is the API running? Try <code>make api-dev</code>, then retry.
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
                <EmptyDescription>
                  Teachers can publish one from the Quizzes page.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {examsQuery.data.map((exam) => (
                <Card key={exam.id} className="gap-3 py-4">
                  <CardHeader className="px-4">
                    <CardTitle className="text-base">{exam.title}</CardTitle>
                    <CardDescription>
                      {exam.question_count} question{exam.question_count === 1 ? "" : "s"}
                    </CardDescription>
                  </CardHeader>
                  <CardFooter className="px-4">
                    <Button
                      size="sm"
                      variant={session.role === "student" ? "default" : "outline"}
                      render={
                        <Link to="/quiz/$examId" params={{ examId: exam.id }} />
                      }
                    >
                      {session.role === "student" ? "Take quiz" : "Preview"}
                      <ArrowRightIcon data-icon="inline-end" />
                    </Button>
                  </CardFooter>
                </Card>
              ))}
            </div>
          )}
        </section>
      </div>
    </AppShell>
  )
}
