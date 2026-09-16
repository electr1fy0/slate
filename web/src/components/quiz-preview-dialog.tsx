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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import type { ExamQuestion, QuestionDraft } from "@/lib/types"
import { Skeleton } from "@/components/ui/skeleton"

type PreviewQuestion = Pick<ExamQuestion, "question" | "options"> & Partial<Pick<QuestionDraft, "answer">>

export function QuizPreviewDialog({
  open,
  onOpenChange,
  title,
  questions,
  loading = false,
  error,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  questions: PreviewQuestion[]
  loading?: boolean
  error?: string
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[min(44rem,calc(100vh-2rem))] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{title || "Quiz preview"}</DialogTitle>
          <DialogDescription>
            This is the learner-facing question flow. Correct answers stay visible only to the teacher preview.
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="flex flex-col gap-3" aria-label="Loading quiz preview">
            <Skeleton className="h-20 rounded-lg" />
            <Skeleton className="h-20 rounded-lg" />
          </div>
        ) : error ? (
          <div className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground">{error}</div>
        ) : questions.length === 0 ? (
          <div className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground">
            Add at least one question to preview this quiz.
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            {questions.map((question, index) => (
              <Card key={`${index}-${question.question}`} size="sm">
                <CardHeader>
                  <CardTitle className="text-sm">Question {index + 1}</CardTitle>
                  <CardDescription className="text-foreground">{question.question || "Untitled question"}</CardDescription>
                </CardHeader>
                <CardContent className="grid gap-2 sm:grid-cols-2">
                  {question.options.map((option, optionIndex) => (
                    <div
                      key={`${optionIndex}-${option}`}
                      className="flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm"
                    >
                      <span className="min-w-0 truncate">
                        <span className="mr-2 font-mono text-xs text-muted-foreground">
                          {String.fromCharCode(65 + optionIndex)}
                        </span>
                        {option || "Empty option"}
                      </span>
                      {question.answer === optionIndex ? <Badge variant="default">Correct</Badge> : null}
                    </div>
                  ))}
                </CardContent>
              </Card>
            ))}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Close preview</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
