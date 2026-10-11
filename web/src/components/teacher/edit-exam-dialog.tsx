import { useEffect, useState } from "react"
import { toast } from "sonner"

import { api, ApiError } from "@/lib/api"
import type { ExamMeta } from "@/lib/types"
import { fromLocalDateTime, toLocalDateTime } from "@/lib/teacher-authoring"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Spinner } from "@/components/ui/spinner"

export interface TeacherClassroomOption {
  id: string
  name: string
}

export function EditExamDialog({
  exam,
  classrooms,
  onOpenChange,
  onSaved,
}: {
  exam: ExamMeta | null
  classrooms: TeacherClassroomOption[]
  onOpenChange: (open: boolean) => void
  onSaved: (updated: ExamMeta) => void
}) {
  const [title, setTitle] = useState("")
  const [instructions, setInstructions] = useState("")
  const [opensAt, setOpensAt] = useState("")
  const [closesAt, setClosesAt] = useState("")
  const [duration, setDuration] = useState("")
  const [maxAttempts, setMaxAttempts] = useState("")
  const [allowLate, setAllowLate] = useState(false)
  const [grace, setGrace] = useState("")
  const [classroomId, setClassroomId] = useState("")
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setTitle(exam?.title ?? "")
    setInstructions(exam?.instructions ?? "")
    setOpensAt(toLocalDateTime(exam?.opens_at))
    setClosesAt(toLocalDateTime(exam?.closes_at))
    setDuration(exam?.duration_minutes ? String(exam.duration_minutes) : "")
    setMaxAttempts(
      exam?.max_attempts !== undefined ? String(exam.max_attempts) : ""
    )
    setAllowLate(Boolean(exam?.allow_late))
    setGrace(exam?.late_grace_minutes ? String(exam.late_grace_minutes) : "")
    setClassroomId(exam?.classroom_id ?? "")
  }, [exam])

  async function save(): Promise<void> {
    if (!exam) return
    if (!title.trim()) {
      toast.error("Add a title before saving")
      return
    }
    const opens = fromLocalDateTime(opensAt)
    const closes = fromLocalDateTime(closesAt)
    if (opens && closes && Date.parse(closes) <= Date.parse(opens)) {
      toast.error("The closing time must be after the opening time")
      return
    }
    const parsedDuration = duration.trim()
      ? Number(duration)
      : exam.duration_minutes
        ? 0
        : undefined
    const parsedAttempts = maxAttempts.trim()
      ? Number(maxAttempts)
      : exam.max_attempts !== undefined
        ? 1
        : undefined
    const parsedGrace = grace.trim()
      ? Number(grace)
      : exam.late_grace_minutes
        ? 0
        : undefined
    if (
      parsedDuration !== undefined &&
      (!Number.isInteger(parsedDuration) ||
        parsedDuration < 0 ||
        parsedDuration > 1440)
    ) {
      toast.error("Duration must be between 0 and 1,440 minutes")
      return
    }
    if (
      parsedAttempts !== undefined &&
      (!Number.isInteger(parsedAttempts) ||
        parsedAttempts < 0 ||
        parsedAttempts > 20)
    ) {
      toast.error("Maximum attempts must be between 0 and 20")
      return
    }
    if (
      parsedGrace !== undefined &&
      (!Number.isInteger(parsedGrace) || parsedGrace < 0 || parsedGrace > 10080)
    ) {
      toast.error("Late grace must be between 0 and 10,080 minutes")
      return
    }
    setSaving(true)
    try {
      const updated = await api.patchExam(exam.id, {
        title: title.trim(),
        instructions: instructions.trim(),
        opens_at: opens ?? "",
        closes_at: closes ?? "",
        duration_minutes: parsedDuration,
        max_attempts: parsedAttempts,
        allow_late: allowLate,
        late_grace_minutes: parsedGrace,
        classroom_id: classroomId,
      })
      onSaved(updated)
      onOpenChange(false)
      toast.success("Quiz settings updated")
    } catch (error) {
      toast.error(
        error instanceof ApiError
          ? error.message
          : "Could not update quiz settings"
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={exam !== null} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[min(44rem,calc(100vh-2rem))] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Edit published quiz</DialogTitle>
          <DialogDescription>
            Change assignment and availability settings. Clearing the closing
            time reopens the quiz.
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="edit-exam-title">Title</FieldLabel>
            <Input
              id="edit-exam-title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="edit-exam-instructions">
              Instructions
            </FieldLabel>
            <Textarea
              id="edit-exam-instructions"
              value={instructions}
              onChange={(event) => setInstructions(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="edit-exam-classroom">
              Assigned classroom
            </FieldLabel>
            <select
              id="edit-exam-classroom"
              className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
              value={classroomId}
              onChange={(event) => setClassroomId(event.target.value)}
            >
              <option value="">No classroom assignment</option>
              {classrooms.map((classroom) => (
                <option key={classroom.id} value={classroom.id}>
                  {classroom.name}
                </option>
              ))}
            </select>
            <FieldDescription>
              Assign a classroom so its learners can access this quiz.
              Unassigned quizzes are visible only to you.
            </FieldDescription>
          </Field>
          <FieldGroup className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="edit-exam-opens">Opens at</FieldLabel>
              <Input
                id="edit-exam-opens"
                type="datetime-local"
                value={opensAt}
                onChange={(event) => setOpensAt(event.target.value)}
              />
              <FieldDescription>Shown in your local timezone.</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-exam-closes">Closes at</FieldLabel>
              <Input
                id="edit-exam-closes"
                type="datetime-local"
                value={closesAt}
                onChange={(event) => setClosesAt(event.target.value)}
              />
              <FieldDescription>
                Clear this field to reopen the quiz.
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-exam-duration">
                Duration in minutes
              </FieldLabel>
              <Input
                id="edit-exam-duration"
                type="number"
                min={0}
                max={1440}
                value={duration}
                onChange={(event) => setDuration(event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-exam-attempts">
                Maximum attempts
              </FieldLabel>
              <Input
                id="edit-exam-attempts"
                type="number"
                min={0}
                max={20}
                value={maxAttempts}
                onChange={(event) => setMaxAttempts(event.target.value)}
              />
              <FieldDescription>
                Leave blank for one attempt, or enter 0 for unlimited attempts.
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-exam-late">
                Allow late submissions
              </FieldLabel>
              <select
                id="edit-exam-late"
                className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
                value={allowLate ? "yes" : "no"}
                onChange={(event) => setAllowLate(event.target.value === "yes")}
              >
                <option value="no">No</option>
                <option value="yes">Yes</option>
              </select>
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-exam-grace">
                Late grace in minutes
              </FieldLabel>
              <Input
                id="edit-exam-grace"
                type="number"
                min={0}
                value={grace}
                onChange={(event) => setGrace(event.target.value)}
                disabled={!allowLate}
              />
            </Field>
          </FieldGroup>
        </FieldGroup>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            Cancel
          </Button>
          <Button onClick={() => void save()} disabled={saving}>
            {saving ? (
              <>
                <Spinner data-icon="inline-start" /> Saving…
              </>
            ) : (
              "Save changes"
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
