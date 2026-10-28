import { useRef, useState } from "react"
import { FileUpIcon } from "lucide-react"

import type { QuestionDraft } from "@/lib/types"
import { parseQuestionImport } from "@/lib/teacher-authoring"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"

export function QuestionImport({
  onImport,
}: {
  onImport: (questions: QuestionDraft[]) => void
}) {
  const [format, setFormat] = useState<"json" | "csv">("json")
  const [source, setSource] = useState("")
  const imported = useRef(false)
  const [result, setResult] = useState<ReturnType<
    typeof parseQuestionImport
  > | null>(null)

  function preview(): void {
    imported.current = false
    setResult(parseQuestionImport(source, format))
  }

  function importQuestions(): void {
    if (
      imported.current ||
      !result ||
      result.errors.length > 0 ||
      result.questions.length === 0
    )
      return
    imported.current = true
    onImport(result.questions)
    setSource("")
    setResult(null)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <FileUpIcon className="size-4" /> Bulk import questions
        </CardTitle>
        <CardDescription>
          Paste JSON or CSV, review the parsed questions, then add them to the
          quiz.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <Field>
          <FieldLabel htmlFor="question-import-format">File format</FieldLabel>
          <select
            id="question-import-format"
            aria-describedby="question-import-help"
            aria-label="Import format"
            className="h-9 w-fit rounded-lg border border-input bg-background px-3 text-sm"
            value={format}
            onChange={(event) => {
              setFormat(event.target.value as "json" | "csv")
              setResult(null)
            }}
          >
            <option value="json">JSON</option>
            <option value="csv">CSV</option>
          </select>
          <FieldDescription id="question-import-help">
            {format === "json"
              ? 'Use an array of objects: {"question":"...","options":["A","B"],"answer":0,"points":1,"feedback":"..."}.'
              : "Use question, option_a through option_f, answer, points, and feedback headers. Answer accepts A–F or a zero-based index."}
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="question-import-source">
            Question data
          </FieldLabel>
          <Textarea
            id="question-import-source"
            aria-describedby="question-import-help"
            spellCheck={false}
            className="min-h-32 font-mono text-xs"
            value={source}
            onChange={(event) => {
              setSource(event.target.value)
              setResult(null)
            }}
            placeholder={
              format === "json"
                ? '[{"question":"What is 2 + 2?","options":["3","4"],"answer":1,"points":2}]'
                : "question,option_a,option_b,answer,points,feedback\nWhat is 2 + 2?,3,4,B,2,Add the numbers"
            }
          />
        </Field>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" disabled={!source.trim()} onClick={preview}>
            Preview import
          </Button>
          {result &&
          result.questions.length > 0 &&
          result.errors.length === 0 ? (
            <Button onClick={importQuestions}>
              Add {result.questions.length} question
              {result.questions.length === 1 ? "" : "s"}
            </Button>
          ) : null}
        </div>
        {result ? (
          <div aria-live="polite" className="flex flex-col gap-2">
            {result.errors.length > 0 ? (
              <Alert variant="destructive">
                <AlertTitle>Fix import errors</AlertTitle>
                <AlertDescription>
                  <ul className="list-disc pl-4">
                    {result.errors.map((error) => (
                      <li key={error}>{error}</li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            ) : (
              <Alert>
                <AlertTitle>
                  Ready to import {result.questions.length} question
                  {result.questions.length === 1 ? "" : "s"}
                </AlertTitle>
                <AlertDescription>
                  Answer indexes and scoring fields are valid.
                </AlertDescription>
              </Alert>
            )}
            {result.questions.map((question, index) => (
              <div
                key={`${index}-${question.question}`}
                className="rounded-lg border p-3 text-sm"
              >
                <p className="font-medium break-words whitespace-pre-wrap">
                  {index + 1}. {question.question}
                </p>
                <ol className="mt-1 space-y-1 text-xs text-muted-foreground">
                  {question.options.map((option, optionIndex) => (
                    <li
                      key={optionIndex}
                      className="break-words whitespace-pre-wrap"
                    >
                      {String.fromCharCode(65 + optionIndex)}. {option}
                      {optionIndex === question.answer ? " (correct)" : ""}
                    </li>
                  ))}
                </ol>
                <p className="mt-1 text-xs text-muted-foreground">
                  {question.points ?? 1} point
                  {(question.points ?? 1) === 1 ? "" : "s"}
                </p>
                {question.feedback ? (
                  <p className="mt-1 text-xs break-words whitespace-pre-wrap text-muted-foreground">
                    Feedback: {question.feedback}
                  </p>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}
