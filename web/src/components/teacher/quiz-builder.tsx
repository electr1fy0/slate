import { useEffect, useMemo, useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  EyeIcon,
  FilePenLineIcon,
  PlusIcon,
  SaveIcon,
  SendIcon,
  Trash2Icon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { getAuth, useAuth } from "@/lib/auth"
import {
  useQuizDrafts,
  removeQuizDraft,
  saveQuizDraft,
} from "@/lib/quiz-drafts"
import {
  removeQuestionFromBank,
  saveQuestionToBank,
  useQuestionBank,
} from "@/lib/question-bank"
import { fromLocalDateTime, toLocalDateTime } from "@/lib/teacher-authoring"
import type { ExamDraft, QuestionDraft, QuizDraft } from "@/lib/types"
import { ConfirmAction } from "@/components/confirm-action"
import { QuizPreviewDialog } from "@/components/quiz-preview-dialog"
import { QuestionImport } from "@/components/teacher/question-import"
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
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"

interface BuilderQuestion extends QuestionDraft {
  key: string
}

interface SyncRecord {
  draft: ExamDraft
  fingerprint: string
  generation: number
  serverId?: string
}

let questionCounter = 0

function blankQuestion(): BuilderQuestion {
  questionCounter += 1
  return {
    key: `q-${questionCounter}`,
    question: "",
    options: ["", ""],
    answer: 0,
  }
}

function optionLetter(index: number): string {
  return String.fromCharCode(65 + index)
}

function fingerprintDraft(draft: ExamDraft): string {
  return JSON.stringify({
    id: draft.id ?? "",
    title: draft.title,
    questions: draft.questions.map(
      ({ question, options, answer, points, feedback }) => ({
        question,
        options,
        answer,
        points: points ?? 1,
        feedback: feedback ?? "",
      })
    ),
    opens_at: draft.opens_at ?? "",
    closes_at: draft.closes_at ?? "",
    duration_minutes: draft.duration_minutes ?? "",
    max_attempts: draft.max_attempts ?? "",
    allow_late: draft.allow_late ?? false,
    late_grace_minutes: draft.late_grace_minutes ?? "",
    classroom_id: draft.classroom_id ?? "",
    instructions: draft.instructions ?? "",
  })
}

function makePage<T>(
  items: T[],
  page: number,
  pageSize: number
): { items: T[]; pages: number } {
  const pages = Math.max(1, Math.ceil(items.length / pageSize))
  return { items: items.slice(page * pageSize, (page + 1) * pageSize), pages }
}

export function QuizBuilder({
  scopeId,
  initialDraft,
  onPublished,
  onDirtyChange,
}: {
  scopeId: string
  initialDraft: ExamDraft | null
  onPublished: () => void
  onDirtyChange?: (dirty: boolean, flushLocal?: () => void) => void
}) {
  const queryClient = useQueryClient()
  const authUserId = useAuth().user?.user_id
  const drafts = useQuizDrafts()
  const localQuestionBank = useQuestionBank()
  const serverDraftsQuery = useQuery({
    queryKey: ["quiz-drafts", scopeId],
    queryFn: api.listQuizDrafts,
    retry: false,
  })
  const classroomsQuery = useQuery({
    queryKey: ["classrooms", scopeId],
    queryFn: api.listClassrooms,
    retry: false,
  })
  const bankQuery = useQuery({
    queryKey: ["question-bank", scopeId],
    queryFn: api.listQuestionBank,
    retry: false,
  })

  const [title, setTitle] = useState("")
  const [customId, setCustomId] = useState("")
  const [instructions, setInstructions] = useState("")
  const [opensAt, setOpensAt] = useState("")
  const [closesAt, setClosesAt] = useState("")
  const [duration, setDuration] = useState("")
  const [maxAttempts, setMaxAttempts] = useState("")
  const [allowLate, setAllowLate] = useState(false)
  const [grace, setGrace] = useState("")
  const [classroomId, setClassroomId] = useState("")
  const [questions, setQuestions] = useState<BuilderQuestion[]>(() => [
    blankQuestion(),
  ])
  const [previewOpen, setPreviewOpen] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [saveStatus, setSaveStatus] = useState<
    "editing" | "saving" | "saved" | "syncing" | "synced" | "local-only"
  >("saved")
  const [savedAt, setSavedAt] = useState<string | null>(null)
  const [localSearch, setLocalSearch] = useState("")
  const [serverSearch, setServerSearch] = useState("")
  const [bankSearch, setBankSearch] = useState("")
  const [localPage, setLocalPage] = useState(0)
  const [serverPage, setServerPage] = useState(0)
  const [bankPage, setBankPage] = useState(0)

  const localDraftIdRef = useRef<string | undefined>(undefined)
  const serverDraftIdRef = useRef<string | undefined>(initialDraft?.id)
  const cleanFingerprintRef = useRef<string | null>(null)
  const builderFingerprintRef = useRef("")
  const generationRef = useRef(0)
  const publishFingerprintRef = useRef<string | null>(null)
  const lastSyncFailureRef = useRef<{
    fingerprint: string
    message: string
  } | null>(null)
  const skipAutoSaveRef = useRef(true)
  const localSaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const queuedSyncRef = useRef<SyncRecord | null>(null)
  const serverSyncPromiseRef = useRef<Promise<void> | null>(null)
  const mountedRef = useRef(false)

  function isActiveScope(): boolean {
    return mountedRef.current && getAuth().user?.user_id === scopeId
  }

  function buildDraft(): ExamDraft {
    return {
      id: customId.trim() || undefined,
      title: title.trim(),
      instructions: instructions.trim() || undefined,
      opens_at: fromLocalDateTime(opensAt),
      closes_at: fromLocalDateTime(closesAt),
      duration_minutes: duration ? Number(duration) : undefined,
      max_attempts: maxAttempts ? Number(maxAttempts) : undefined,
      allow_late: allowLate,
      late_grace_minutes: grace ? Number(grace) : undefined,
      classroom_id: classroomId || undefined,
      questions: questions.map(
        ({ question, options, answer, points, feedback }) => ({
          question: question.trim(),
          options: options.map((option) => option.trim()),
          answer,
          points,
          feedback: feedback?.trim() || undefined,
        })
      ),
    }
  }

  const currentDraft = buildDraft()
  const currentFingerprint = fingerprintDraft(currentDraft)
  const latestDraftRef = useRef(currentDraft)
  latestDraftRef.current = currentDraft
  builderFingerprintRef.current = currentFingerprint

  function writeLocal(draft: ExamDraft): void {
    if (!isActiveScope()) return
    const saved = saveQuizDraft(
      { ...draft, title: draft.title.trim() || "Untitled draft" },
      localDraftIdRef.current
    )
    localDraftIdRef.current = saved.draft_id
    setSavedAt(saved.updated_at)
  }

  async function drainServerQueue(): Promise<void> {
    while (queuedSyncRef.current) {
      if (!isActiveScope()) {
        queuedSyncRef.current = null
        return
      }
      const record = queuedSyncRef.current
      queuedSyncRef.current = null
      try {
        let synced: QuizDraft
        const currentServerId =
          record.generation === generationRef.current
            ? serverDraftIdRef.current
            : record.serverId
        if (currentServerId) {
          try {
            synced = await api.updateQuizDraft(currentServerId, record.draft)
          } catch (error) {
            if (!(error instanceof ApiError) || error.status !== 404)
              throw error
            if (!isActiveScope()) {
              queuedSyncRef.current = null
              return
            }
            synced = await api.createQuizDraft(record.draft)
          }
        } else {
          synced = await api.createQuizDraft(record.draft)
        }
        if (!isActiveScope()) {
          queuedSyncRef.current = null
          return
        }
        if (record.generation === generationRef.current)
          serverDraftIdRef.current = synced.id
        void queryClient.invalidateQueries({ queryKey: ["quiz-drafts"] })
        if (
          record.generation === generationRef.current &&
          builderFingerprintRef.current === record.fingerprint
        ) {
          cleanFingerprintRef.current = record.fingerprint
          setDirty(false)
          onDirtyChange?.(false, () => undefined)
          setSaveStatus("synced")
        }
      } catch {
        if (!isActiveScope()) {
          queuedSyncRef.current = null
          return
        }
        if (
          record.generation === generationRef.current &&
          builderFingerprintRef.current === record.fingerprint
        ) {
          lastSyncFailureRef.current = {
            fingerprint: record.fingerprint,
            message: "The server could not be reached.",
          }
          setSaveStatus("local-only")
        }
      }
    }
  }

  function queueServerSync(
    draft: ExamDraft,
    fingerprint: string
  ): Promise<void> {
    if (!isActiveScope()) return Promise.resolve()
    lastSyncFailureRef.current = null
    queuedSyncRef.current = {
      draft,
      fingerprint,
      generation: generationRef.current,
      serverId: serverDraftIdRef.current,
    }
    if (!serverSyncPromiseRef.current) {
      const promise = drainServerQueue()
      serverSyncPromiseRef.current = promise
      void promise.then(
        () => {
          if (serverSyncPromiseRef.current === promise)
            serverSyncPromiseRef.current = null
          if (queuedSyncRef.current)
            void queueServerSync(
              queuedSyncRef.current.draft,
              queuedSyncRef.current.fingerprint
            )
        },
        () => {
          if (serverSyncPromiseRef.current === promise)
            serverSyncPromiseRef.current = null
          if (queuedSyncRef.current)
            void queueServerSync(
              queuedSyncRef.current.draft,
              queuedSyncRef.current.fingerprint
            )
        }
      )
    }
    return serverSyncPromiseRef.current
  }

  function isValidForSync(draft: ExamDraft): boolean {
    return (
      Boolean(draft.title.trim()) &&
      draft.questions.length > 0 &&
      draft.questions.every(
        (question) =>
          Boolean(question.question.trim()) &&
          question.options.length >= 2 &&
          question.options.every((option) => Boolean(option.trim())) &&
          Number.isInteger(question.answer) &&
          question.answer >= 0 &&
          question.answer < question.options.length &&
          (question.points === undefined ||
            (Number.isFinite(question.points) &&
              question.points >= 1 &&
              question.points <= 1000))
      )
    )
  }

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      queuedSyncRef.current = null
      if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
    }
  }, [])

  useEffect(() => {
    if (authUserId === scopeId) return
    queuedSyncRef.current = null
    if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
  }, [authUserId, scopeId])

  useEffect(() => {
    const draft = initialDraft
    generationRef.current += 1
    lastSyncFailureRef.current = null
    const seededQuestions = draft?.questions.length
      ? draft.questions.map((question, index) => ({
          ...question,
          options: [...question.options],
          key: `seed-${index}-${Date.now()}`,
        }))
      : [blankQuestion()]
    setTitle(draft?.title ?? "")
    setCustomId(draft?.id ?? "")
    setInstructions(draft?.instructions ?? "")
    setOpensAt(toLocalDateTime(draft?.opens_at))
    setClosesAt(toLocalDateTime(draft?.closes_at))
    setDuration(draft?.duration_minutes ? String(draft.duration_minutes) : "")
    setMaxAttempts(
      draft?.max_attempts !== undefined ? String(draft.max_attempts) : ""
    )
    setAllowLate(Boolean(draft?.allow_late))
    setGrace(draft?.late_grace_minutes ? String(draft.late_grace_minutes) : "")
    setClassroomId(draft?.classroom_id ?? "")
    setQuestions(seededQuestions)
    localDraftIdRef.current = undefined
    serverDraftIdRef.current = draft?.id
    const baseline: ExamDraft = draft
      ? {
          ...draft,
          questions: seededQuestions.map(
            ({ question, options, answer, points, feedback }) => ({
              question,
              options,
              answer,
              points,
              feedback,
            })
          ),
        }
      : {
          title: "",
          questions: [{ question: "", options: ["", ""], answer: 0 }],
        }
    cleanFingerprintRef.current = fingerprintDraft(baseline)
    skipAutoSaveRef.current = true
  }, [initialDraft])

  useEffect(() => {
    if (skipAutoSaveRef.current) {
      skipAutoSaveRef.current = false
      setDirty(false)
      onDirtyChange?.(false)
      return
    }
    const isDirty = currentFingerprint !== cleanFingerprintRef.current
    setDirty(isDirty)
    const flushLocal = () => {
      if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
      writeLocal(latestDraftRef.current)
      setSaveStatus("saved")
    }
    onDirtyChange?.(isDirty, flushLocal)
    if (isDirty) setSaveStatus("editing")
    if (!isDirty) return
    if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
    localSaveTimerRef.current = setTimeout(() => {
      setSaveStatus("saving")
      writeLocal(currentDraft)
      setSaveStatus("saved")
      if (isValidForSync(currentDraft)) {
        setSaveStatus("syncing")
        void queueServerSync(currentDraft, currentFingerprint)
      }
    }, 700)
    return () => {
      if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
    }
    // The fingerprint intentionally covers every editable value in currentDraft.
  }, [currentFingerprint])

  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => {
      if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
      writeLocal(latestDraftRef.current)
      event.preventDefault()
      event.returnValue = ""
    }
    window.addEventListener("beforeunload", warn)
    return () => window.removeEventListener("beforeunload", warn)
  }, [dirty])

  function confirmDiscard(): boolean {
    if (!dirty) return true
    const confirmed = window.confirm(
      "This quiz has changes that are not synced yet. Load another draft and replace the current builder?"
    )
    if (confirmed && localSaveTimerRef.current) {
      clearTimeout(localSaveTimerRef.current)
      writeLocal(latestDraftRef.current)
    }
    return confirmed
  }

  function replaceDraft(
    draft: ExamDraft,
    nextLocalDraftId?: string,
    nextServerDraftId?: string
  ): void {
    generationRef.current += 1
    lastSyncFailureRef.current = null
    setTitle(draft.title)
    setCustomId(draft.id ?? "")
    setInstructions(draft.instructions ?? "")
    setOpensAt(toLocalDateTime(draft.opens_at))
    setClosesAt(toLocalDateTime(draft.closes_at))
    setDuration(draft.duration_minutes ? String(draft.duration_minutes) : "")
    setMaxAttempts(
      draft.max_attempts !== undefined ? String(draft.max_attempts) : ""
    )
    setAllowLate(Boolean(draft.allow_late))
    setGrace(draft.late_grace_minutes ? String(draft.late_grace_minutes) : "")
    setClassroomId(draft.classroom_id ?? "")
    const seededQuestions = draft.questions.map((question, index) => ({
      ...question,
      options: [...question.options],
      key: `loaded-${index}-${Date.now()}`,
    }))
    setQuestions(seededQuestions.length ? seededQuestions : [blankQuestion()])
    const normalizedDraft = {
      ...draft,
      questions: seededQuestions.length
        ? seededQuestions
        : [{ question: "", options: ["", ""], answer: 0 }],
    }
    cleanFingerprintRef.current = fingerprintDraft(normalizedDraft)
    localDraftIdRef.current = nextLocalDraftId
    serverDraftIdRef.current = nextServerDraftId
    setDirty(false)
    setSaveStatus("saved")
    skipAutoSaveRef.current = true
    onDirtyChange?.(false, () => undefined)
  }

  function loadLocalDraft(draft: ExamDraft & { draft_id: string }): void {
    if (!confirmDiscard()) return
    replaceDraft(draft, draft.draft_id)
    toast.info("Draft loaded")
  }

  function loadServerDraft(draft: QuizDraft): void {
    if (!confirmDiscard()) return
    replaceDraft(draft, undefined, draft.id)
    toast.info("Server draft loaded")
  }

  function patchQuestion(key: string, patch: Partial<BuilderQuestion>): void {
    setQuestions((current) =>
      current.map((question) =>
        question.key === key ? { ...question, ...patch } : question
      )
    )
  }

  function addOption(key: string): void {
    setQuestions((current) =>
      current.map((question) =>
        question.key === key && question.options.length < 6
          ? { ...question, options: [...question.options, ""] }
          : question
      )
    )
  }

  function removeOption(key: string, index: number): void {
    const current = questions.find((question) => question.key === key)
    if (!current || current.options.length <= 2) return
    const previous = { ...current, options: [...current.options] }
    const options = current.options.filter((_, position) => position !== index)
    const answer =
      current.answer === index
        ? 0
        : current.answer > index
          ? current.answer - 1
          : current.answer
    patchQuestion(key, { options, answer })
    toast("Option removed", {
      action: {
        label: "Undo",
        onClick: () =>
          patchQuestion(key, {
            options: previous.options,
            answer: previous.answer,
          }),
      },
    })
  }

  function removeBuilderQuestion(question: BuilderQuestion): void {
    const index = questions.findIndex((item) => item.key === question.key)
    if (index < 0 || questions.length <= 1) return
    setQuestions((current) =>
      current.filter((item) => item.key !== question.key)
    )
    toast("Question removed", {
      action: {
        label: "Undo",
        onClick: () =>
          setQuestions((current) => {
            if (current.some((item) => item.key === question.key))
              return current
            const next = [...current]
            next.splice(Math.min(index, next.length), 0, question)
            return next
          }),
      },
    })
  }

  function moveQuestion(index: number, direction: -1 | 1): void {
    setQuestions((current) => {
      const nextIndex = index + direction
      if (nextIndex < 0 || nextIndex >= current.length) return current
      const next = [...current]
      ;[next[index], next[nextIndex]] = [next[nextIndex], next[index]]
      return next
    })
  }

  async function saveQuestion(question: BuilderQuestion): Promise<void> {
    const options = question.options.map((option) => option.trim())
    const points = question.points ?? 1
    if (
      !isActiveScope() ||
      !question.question.trim() ||
      options.length < 2 ||
      options.length > 6 ||
      options.some((option) => !option) ||
      !Number.isInteger(question.answer) ||
      question.answer < 0 ||
      question.answer >= options.length ||
      !Number.isFinite(points) ||
      points < 1 ||
      points > 1000
    ) {
      toast.error(
        "Add a valid prompt, two to six options, a correct answer, and 1–1,000 points before saving to the bank"
      )
      return
    }
    const payload: QuestionDraft = {
      question: question.question.trim(),
      options,
      answer: question.answer,
      points,
      feedback: question.feedback?.trim() || undefined,
    }
    const local = saveQuestionToBank(payload)
    try {
      await api.createBankQuestion(payload)
      if (!isActiveScope()) return
      removeQuestionFromBank(local.id)
      void queryClient.invalidateQueries({ queryKey: ["question-bank"] })
      toast.success("Question synced to your bank")
    } catch (error) {
      if (!isActiveScope()) return
      toast.warning("Question saved on this browser", {
        description:
          error instanceof ApiError
            ? error.message
            : "The bank service could not be reached.",
      })
    }
  }

  function useBankQuestion(question: QuestionDraft & { id?: string }): void {
    setQuestions((current) => [
      ...current,
      {
        ...question,
        options: [...question.options],
        key: `bank-${question.id ?? "local"}-${Date.now()}`,
      },
    ])
    toast.info("Question added to this quiz")
  }

  async function deleteBankItem(id: string): Promise<void> {
    if (!isActiveScope()) return
    if ((bankQuery.data ?? []).some((item) => item.id === id)) {
      await api.deleteBankQuestion(id)
      if (!isActiveScope()) return
      void queryClient.invalidateQueries({ queryKey: ["question-bank"] })
    }
    removeQuestionFromBank(id)
    toast.success("Question removed from the bank")
  }

  async function saveDraft(): Promise<void> {
    if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
    const draft = buildDraft()
    writeLocal(draft)
    if (!isValidForSync(draft)) {
      setSaveStatus("local-only")
      toast.info("Draft saved locally", {
        description:
          "Add a title, prompt, and two options to sync it to your workspace.",
      })
      return
    }
    setSaveStatus("syncing")
    await queueServerSync(draft, currentFingerprint)
    if (!isActiveScope()) return
    if (
      builderFingerprintRef.current === currentFingerprint &&
      saveStatus !== "synced"
    ) {
      toast.success("Draft saved")
    }
  }

  const publishMutation = useMutation({
    mutationFn: async () => {
      if (localSaveTimerRef.current) clearTimeout(localSaveTimerRef.current)
      const draft = buildDraft()
      publishFingerprintRef.current = currentFingerprint
      writeLocal(draft)
      await queueServerSync(draft, currentFingerprint)
      if (!isActiveScope())
        throw new Error(
          "Your sign-in changed before the quiz could be published. Review it again in the current workspace."
        )
      if (lastSyncFailureRef.current?.fingerprint === currentFingerprint) {
        throw new Error(
          `The draft is saved locally but could not sync: ${lastSyncFailureRef.current.message}`
        )
      }
      const draftIdToPublish = serverDraftIdRef.current
      if (!draftIdToPublish)
        throw new Error(
          "The draft could not sync to the server before publishing."
        )
      return api.publishQuizDraft(draftIdToPublish)
    },
    onSuccess: (created) => {
      if (!isActiveScope()) return
      toast.success(`Published "${created.title}"`, {
        description: `${created.question_count} questions · id ${created.id}`,
      })
      void queryClient.invalidateQueries({ queryKey: ["exams"] })
      void queryClient.invalidateQueries({ queryKey: ["quiz-drafts"] })
      if (builderFingerprintRef.current !== publishFingerprintRef.current) {
        serverDraftIdRef.current = undefined
        setDirty(true)
        setSaveStatus("editing")
        onDirtyChange?.(true, () => writeLocal(latestDraftRef.current))
        return
      }
      cleanFingerprintRef.current = fingerprintDraft({
        title: "",
        questions: [{ question: "", options: ["", ""], answer: 0 }],
      })
      skipAutoSaveRef.current = true
      setTitle("")
      setCustomId("")
      setInstructions("")
      setOpensAt("")
      setClosesAt("")
      setDuration("")
      setMaxAttempts("")
      setAllowLate(false)
      setGrace("")
      setClassroomId("")
      setQuestions([blankQuestion()])
      localDraftIdRef.current = undefined
      serverDraftIdRef.current = undefined
      setDirty(false)
      onDirtyChange?.(false, () => undefined)
      onPublished()
    },
    onError: (error) => {
      if (!isActiveScope()) return
      if (error instanceof ApiError && error.status === 401) {
        toast.error("Please sign in again to publish")
        return
      }
      toast.error(
        error instanceof ApiError ? error.message : "Could not publish quiz"
      )
    },
  })

  const titleReady = title.trim().length > 0
  const questionsReady =
    questions.length > 0 &&
    questions.every(
      (question) =>
        question.question.trim().length > 0 &&
        question.options.length >= 2 &&
        question.options.every((option) => option.trim().length > 0) &&
        Number.isInteger(question.answer) &&
        question.answer >= 0 &&
        question.answer < question.options.length &&
        (question.points === undefined ||
          (Number.isFinite(question.points) &&
            question.points >= 1 &&
            question.points <= 1000))
    )
  const openInstant = opensAt ? new Date(opensAt).getTime() : null
  const closeInstant = closesAt ? new Date(closesAt).getTime() : null
  const validWindow =
    openInstant === null || closeInstant === null || closeInstant > openInstant
  const validDuration =
    !duration || (Number(duration) > 0 && Number(duration) <= 1440)
  const validAttempts =
    !maxAttempts || (Number(maxAttempts) >= 0 && Number(maxAttempts) <= 20)
  const validGrace = !grace || (Number(grace) >= 0 && Number(grace) <= 10080)
  const canPublish =
    titleReady &&
    questionsReady &&
    validWindow &&
    validDuration &&
    validAttempts &&
    validGrace &&
    !publishMutation.isPending

  const localDraftsFiltered = drafts.filter((draft) =>
    `${draft.title} ${draft.id ?? ""}`
      .toLowerCase()
      .includes(localSearch.trim().toLowerCase())
  )
  const localDraftPage = makePage(localDraftsFiltered, localPage, 5)
  const serverDrafts = serverDraftsQuery.data ?? []
  const serverDraftsFiltered = serverDrafts.filter((draft) =>
    `${draft.title} ${draft.id}`
      .toLowerCase()
      .includes(serverSearch.trim().toLowerCase())
  )
  const serverDraftPage = makePage(serverDraftsFiltered, serverPage, 5)
  const remoteBank = bankQuery.data ?? []
  const bankItems = useMemo(() => {
    const remoteContent = new Set(
      remoteBank.map((item) =>
        JSON.stringify([item.question, item.options, item.answer])
      )
    )
    return [
      ...remoteBank,
      ...localQuestionBank.filter(
        (item) =>
          !remoteContent.has(
            JSON.stringify([item.question, item.options, item.answer])
          )
      ),
    ]
  }, [localQuestionBank, remoteBank])
  const bankItemsFiltered = bankItems.filter((item) =>
    item.question.toLowerCase().includes(bankSearch.trim().toLowerCase())
  )
  const bankPageItems = makePage(bankItemsFiltered, bankPage, 5)

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Quiz details</CardTitle>
          <CardDescription>
            Set the assignment, access window, and learner instructions.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <FieldGroup>
            <Field
              data-invalid={!titleReady && title.length > 0 ? true : undefined}
            >
              <FieldLabel htmlFor="quiz-title">Title</FieldLabel>
              <Input
                id="quiz-title"
                placeholder="CS101 — Week 4 recap"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="quiz-id">Custom ID (optional)</FieldLabel>
              <Input
                id="quiz-id"
                placeholder="auto-generated from the title"
                value={customId}
                onChange={(event) => setCustomId(event.target.value)}
                autoCapitalize="none"
                spellCheck={false}
              />
              <FieldDescription>
                A slug; conflicts are rejected with a 409.
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="quiz-classroom">
                Assigned classroom
              </FieldLabel>
              <select
                id="quiz-classroom"
                aria-label="Assigned classroom"
                className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
                value={classroomId}
                onChange={(event) => setClassroomId(event.target.value)}
              >
                <option value="">No classroom assignment</option>
                {(classroomsQuery.data ?? []).map((classroom) => (
                  <option key={classroom.id} value={classroom.id}>
                    {classroom.name}
                  </option>
                ))}
              </select>
              <FieldDescription>
                Assign a classroom so its learners can access this quiz.
                Unassigned quizzes are visible only to you.
              </FieldDescription>
              {classroomsQuery.isError ? (
                <FieldDescription>
                  Could not load your classrooms. Try refreshing the page.
                </FieldDescription>
              ) : null}
            </Field>
            <Field>
              <FieldLabel htmlFor="quiz-instructions">
                Instructions (optional)
              </FieldLabel>
              <Textarea
                id="quiz-instructions"
                value={instructions}
                onChange={(event) => setInstructions(event.target.value)}
                placeholder="Add context or directions students should read before starting."
              />
            </Field>
          </FieldGroup>
          <FieldGroup className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="quiz-opens">Opens at</FieldLabel>
              <Input
                id="quiz-opens"
                type="datetime-local"
                value={opensAt}
                onChange={(event) => setOpensAt(event.target.value)}
              />
              <FieldDescription>
                Entered in your local timezone.
              </FieldDescription>
            </Field>
            <Field data-invalid={!validWindow ? true : undefined}>
              <FieldLabel htmlFor="quiz-closes">Closes at</FieldLabel>
              <Input
                id="quiz-closes"
                type="datetime-local"
                value={closesAt}
                onChange={(event) => setClosesAt(event.target.value)}
              />
              <FieldDescription>
                {validWindow
                  ? "Leave blank for no closing time."
                  : "Must be after the opening time."}
              </FieldDescription>
            </Field>
            <Field data-invalid={!validDuration ? true : undefined}>
              <FieldLabel htmlFor="quiz-duration">
                Duration in minutes
              </FieldLabel>
              <Input
                id="quiz-duration"
                type="number"
                min={1}
                max={1440}
                value={duration}
                onChange={(event) => setDuration(event.target.value)}
                placeholder="No limit"
              />
              <FieldDescription>
                Optional; up to 1,440 minutes.
              </FieldDescription>
            </Field>
            <Field data-invalid={!validAttempts ? true : undefined}>
              <FieldLabel htmlFor="quiz-max-attempts">
                Maximum attempts
              </FieldLabel>
              <Input
                id="quiz-max-attempts"
                type="number"
                min={0}
                max={20}
                value={maxAttempts}
                onChange={(event) => setMaxAttempts(event.target.value)}
                placeholder="Default: 1"
              />
              <FieldDescription>
                Leave blank for one attempt, enter 0 for unlimited attempts, or
                set a limit up to 20.
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="quiz-allow-late">
                Allow late submissions
              </FieldLabel>
              <label className="flex h-9 items-center gap-2 text-sm">
                <input
                  id="quiz-allow-late"
                  type="checkbox"
                  checked={allowLate}
                  onChange={(event) => setAllowLate(event.target.checked)}
                />
                Accept late work
              </label>
            </Field>
            <Field data-invalid={!validGrace ? true : undefined}>
              <FieldLabel htmlFor="quiz-late-grace">
                Late grace in minutes
              </FieldLabel>
              <Input
                id="quiz-late-grace"
                type="number"
                min={0}
                max={10080}
                value={grace}
                onChange={(event) => setGrace(event.target.value)}
                placeholder="0"
                disabled={!allowLate}
              />
              <FieldDescription>
                Blank or 0 allows late work without a cutoff. Otherwise, up to 7
                days.
              </FieldDescription>
            </Field>
          </FieldGroup>
        </CardContent>
      </Card>

      <Card size="sm">
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div className="flex min-w-0 flex-col gap-1">
            <CardTitle className="flex items-center gap-2 text-sm">
              <FilePenLineIcon data-icon="inline-start" /> Drafts
            </CardTitle>
            <CardDescription>
              Drafts are saved to this browser and synced to your teacher
              workspace when valid.
            </CardDescription>
          </div>
          <Badge variant="secondary">
            {drafts.length + serverDrafts.length}
          </Badge>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {drafts.length > 0 ? (
            <section className="flex flex-col gap-2" aria-label="Local drafts">
              <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-medium">Local drafts</h3>
                <Input
                  aria-label="Search local drafts"
                  className="h-8 max-w-56"
                  type="search"
                  value={localSearch}
                  onChange={(event) => {
                    setLocalSearch(event.target.value)
                    setLocalPage(0)
                  }}
                  placeholder="Search drafts"
                />
              </div>
              {localDraftPage.items.length ? (
                localDraftPage.items.map((draft) => (
                  <div
                    key={draft.draft_id}
                    className="flex flex-wrap items-center justify-between gap-2 rounded-lg border p-2"
                  >
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">
                        {draft.title || "Untitled draft"}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        Updated {new Date(draft.updated_at).toLocaleString()}
                      </p>
                    </div>
                    <div className="flex items-center gap-1">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => loadLocalDraft(draft)}
                      >
                        Load
                      </Button>
                      <ConfirmAction
                        title={`Delete “${draft.title || "Untitled draft"}”?`}
                        description="This removes the draft from this browser."
                        confirmLabel="Delete draft"
                        onConfirm={() => {
                          if (!isActiveScope()) return
                          removeQuizDraft(draft.draft_id)
                          toast.success("Local draft deleted")
                        }}
                      >
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          aria-label={`Delete ${draft.title || "Untitled draft"}`}
                        >
                          <Trash2Icon />
                        </Button>
                      </ConfirmAction>
                    </div>
                  </div>
                ))
              ) : (
                <p className="text-xs text-muted-foreground">
                  No local drafts match that search.
                </p>
              )}
              <PageControls
                page={localPage}
                pages={localDraftPage.pages}
                onPageChange={setLocalPage}
                label="local drafts"
              />
            </section>
          ) : null}
          {serverDraftsQuery.isPending ? (
            <Skeleton className="h-10 w-full" />
          ) : serverDrafts.length > 0 ? (
            <section
              className="flex flex-col gap-2 border-t pt-3"
              aria-label="Server drafts"
            >
              <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-medium">
                  Teacher workspace drafts
                </h3>
                <Input
                  aria-label="Search server drafts"
                  className="h-8 max-w-56"
                  type="search"
                  value={serverSearch}
                  onChange={(event) => {
                    setServerSearch(event.target.value)
                    setServerPage(0)
                  }}
                  placeholder="Search drafts"
                />
              </div>
              {serverDraftPage.items.length ? (
                serverDraftPage.items.map((draft) => (
                  <div
                    key={draft.id}
                    className="flex flex-wrap items-center justify-between gap-2 rounded-lg border p-2"
                  >
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">
                        {draft.title}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        Updated{" "}
                        {draft.updated_at
                          ? new Date(draft.updated_at).toLocaleString()
                          : "recently"}
                      </p>
                    </div>
                    <div className="flex items-center gap-1">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => loadServerDraft(draft)}
                      >
                        Load
                      </Button>
                      <ConfirmAction
                        title={`Delete “${draft.title}”?`}
                        description="This permanently removes this server draft."
                        confirmLabel="Delete draft"
                        onConfirm={async () => {
                          if (!isActiveScope()) return
                          await api.deleteQuizDraft(draft.id)
                          if (!isActiveScope()) return
                          void queryClient.invalidateQueries({
                            queryKey: ["quiz-drafts"],
                          })
                          toast.success("Server draft deleted")
                        }}
                      >
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          aria-label={`Delete server draft ${draft.title}`}
                        >
                          <Trash2Icon />
                        </Button>
                      </ConfirmAction>
                    </div>
                  </div>
                ))
              ) : (
                <p className="text-xs text-muted-foreground">
                  No server drafts match that search.
                </p>
              )}
              <PageControls
                page={serverPage}
                pages={serverDraftPage.pages}
                onPageChange={setServerPage}
                label="server drafts"
              />
            </section>
          ) : null}
          {serverDraftsQuery.isError ? (
            <p className="text-xs text-muted-foreground">
              Server drafts could not be loaded. Local drafts remain available.
            </p>
          ) : null}
        </CardContent>
      </Card>

      <Card size="sm">
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div className="flex min-w-0 flex-col gap-1">
            <CardTitle className="text-sm">Question bank</CardTitle>
            <CardDescription>
              Reuse questions saved to your workspace or this browser.
            </CardDescription>
          </div>
          <Badge variant="secondary">{bankItems.length}</Badge>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          <Input
            aria-label="Search question bank"
            type="search"
            value={bankSearch}
            onChange={(event) => {
              setBankSearch(event.target.value)
              setBankPage(0)
            }}
            placeholder="Search question bank"
          />
          {bankQuery.isPending && localQuestionBank.length === 0 ? (
            <Skeleton className="h-10 w-full" />
          ) : bankPageItems.items.length > 0 ? (
            bankPageItems.items.map((question) => (
              <div
                key={question.id}
                className="flex items-center justify-between gap-2 rounded-lg border p-2"
              >
                <p className="min-w-0 truncate text-sm">{question.question}</p>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => useBankQuestion(question)}
                  >
                    Use
                  </Button>
                  <ConfirmAction
                    title={`Delete “${question.question}”?`}
                    description="This removes the question from your bank. Questions already used in quizzes stay there."
                    confirmLabel="Delete question"
                    onConfirm={() => deleteBankItem(question.id)}
                  >
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Delete bank question ${question.question}`}
                    >
                      <Trash2Icon />
                    </Button>
                  </ConfirmAction>
                </div>
              </div>
            ))
          ) : (
            <p className="text-xs text-muted-foreground">
              {bankItems.length
                ? "No questions match that search."
                : "Save a completed question to reuse it later."}
            </p>
          )}
          <PageControls
            page={bankPage}
            pages={bankPageItems.pages}
            onPageChange={setBankPage}
            label="question bank"
          />
          {bankQuery.isError ? (
            <p className="text-xs text-muted-foreground">
              Showing questions saved in this browser; workspace bank sync is
              unavailable.
            </p>
          ) : null}
        </CardContent>
      </Card>

      <QuestionImport
        onImport={(imported) => {
          setQuestions((current) => {
            const isEmptyStarter =
              current.length === 1 &&
              !current[0].question.trim() &&
              current[0].options.every((option) => !option.trim())
            const newQuestions = imported.map((question) => ({
              ...question,
              options: [...question.options],
              key: `import-${(questionCounter += 1)}`,
            }))
            return isEmptyStarter ? newQuestions : [...current, ...newQuestions]
          })
          toast.success(
            `Added ${imported.length} question${imported.length === 1 ? "" : "s"}`
          )
        }}
      />

      {questions.map((question, index) => (
        <Card key={question.key}>
          <CardHeader className="flex-row items-start justify-between space-y-0">
            <div className="flex flex-col gap-1">
              <CardTitle className="text-base">Question {index + 1}</CardTitle>
              <CardDescription>
                Multiple choice; pick the correct option below.
              </CardDescription>
            </div>
            <div className="flex items-center gap-1">
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`Move question ${index + 1} up`}
                disabled={index === 0}
                onClick={() => moveQuestion(index, -1)}
              >
                <ArrowUpIcon />
              </Button>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`Move question ${index + 1} down`}
                disabled={index === questions.length - 1}
                onClick={() => moveQuestion(index, 1)}
              >
                <ArrowDownIcon />
              </Button>
              {questions.length > 1 ? (
                <ConfirmAction
                  title={`Remove question ${index + 1}?`}
                  description="The prompt, options, scoring, and feedback will be removed from this quiz. You can undo this removal from the notification."
                  confirmLabel="Remove question"
                  onConfirm={() => removeBuilderQuestion(question)}
                >
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Remove question ${index + 1}`}
                  >
                    <Trash2Icon />
                  </Button>
                </ConfirmAction>
              ) : null}
              <Button
                size="sm"
                variant="outline"
                onClick={() => void saveQuestion(question)}
              >
                <SaveIcon data-icon="inline-start" />
                Bank
              </Button>
            </div>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <Field>
              <FieldLabel htmlFor={`question-text-${question.key}`}>
                Prompt
              </FieldLabel>
              <Textarea
                id={`question-text-${question.key}`}
                placeholder="What does RPO measure?"
                value={question.question}
                onChange={(event) =>
                  patchQuestion(question.key, { question: event.target.value })
                }
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
                  <InputGroup>
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
                        onClick={() =>
                          patchQuestion(question.key, { answer: optionIndex })
                        }
                        variant={
                          optionIndex === question.answer ? "default" : "ghost"
                        }
                      >
                        <EyeIcon />
                      </InputGroupButton>
                      {question.options.length > 2 ? (
                        <ConfirmAction
                          title={`Remove option ${optionLetter(optionIndex)}?`}
                          description="This removes the option and adjusts the correct answer index when needed. You can undo it from the notification."
                          confirmLabel="Remove option"
                          onConfirm={() =>
                            removeOption(question.key, optionIndex)
                          }
                        >
                          <InputGroupButton
                            aria-label={`Remove option ${optionLetter(optionIndex)}`}
                          >
                            <Trash2Icon />
                          </InputGroupButton>
                        </ConfirmAction>
                      ) : null}
                    </InputGroupAddon>
                  </InputGroup>
                </Field>
              ))}
            </FieldGroup>
            {question.options.length < 6 ? (
              <Button
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => addOption(question.key)}
              >
                <PlusIcon data-icon="inline-start" />
                Add option
              </Button>
            ) : null}
            <FieldGroup className="grid gap-4 sm:grid-cols-[8rem_minmax(0,1fr)]">
              <Field>
                <FieldLabel htmlFor={`points-${question.key}`}>
                  Points
                </FieldLabel>
                <Input
                  id={`points-${question.key}`}
                  type="number"
                  min={1}
                  max={1000}
                  value={question.points ?? 1}
                  onChange={(event) =>
                    patchQuestion(question.key, {
                      points: Math.max(
                        1,
                        Math.min(1000, Number(event.target.value) || 1)
                      ),
                    })
                  }
                />
              </Field>
              <Field>
                <FieldLabel htmlFor={`feedback-${question.key}`}>
                  Feedback (optional)
                </FieldLabel>
                <Textarea
                  id={`feedback-${question.key}`}
                  placeholder="Explain why the answer is correct."
                  value={question.feedback ?? ""}
                  onChange={(event) =>
                    patchQuestion(question.key, {
                      feedback: event.target.value,
                    })
                  }
                />
              </Field>
            </FieldGroup>
          </CardContent>
        </Card>
      ))}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            onClick={() =>
              setQuestions((current) => [...current, blankQuestion()])
            }
          >
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
        <div className="flex flex-wrap items-center gap-2">
          <p
            role="status"
            aria-live="polite"
            className="text-xs text-muted-foreground"
          >
            {saveStatus === "editing"
              ? "Changes not synced"
              : saveStatus === "saving"
                ? "Saving locally…"
                : saveStatus === "saved"
                  ? `Saved locally${savedAt ? ` at ${new Date(savedAt).toLocaleTimeString()}` : ""}`
                  : saveStatus === "syncing"
                    ? "Syncing draft…"
                    : saveStatus === "synced"
                      ? "Saved and synced"
                      : "Saved locally · sync unavailable"}
          </p>
          <Button
            disabled={!canPublish}
            onClick={() => publishMutation.mutate()}
          >
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
      {!canPublish && !publishMutation.isPending ? (
        <p className="text-right text-xs text-muted-foreground">
          Add a title and complete each question, and check the time and scoring
          limits before publishing.
        </p>
      ) : null}
      <QuizPreviewDialog
        open={previewOpen}
        onOpenChange={setPreviewOpen}
        title={title}
        questions={questions}
      />
    </div>
  )
}

function PageControls({
  page,
  pages,
  onPageChange,
  label,
}: {
  page: number
  pages: number
  onPageChange: (page: number) => void
  label: string
}) {
  if (pages <= 1) return null
  return (
    <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
      <span>
        Page {page + 1} of {pages} {label}
      </span>
      <div className="flex gap-1">
        <Button
          size="sm"
          variant="outline"
          disabled={page === 0}
          onClick={() => onPageChange(page - 1)}
        >
          Previous
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={page >= pages - 1}
          onClick={() => onPageChange(page + 1)}
        >
          Next
        </Button>
      </div>
    </div>
  )
}
