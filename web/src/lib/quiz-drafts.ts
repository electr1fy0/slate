import { useSyncExternalStore } from "react"

import { getAuth, useAuth } from "./auth"
import type { ExamDraft } from "./types"
import {
  isRecord,
  localId,
  nonEmpty,
  nonNegativeInteger,
  uniqueBy,
  validDate,
} from "./local-validation"

export interface SavedQuizDraft extends ExamDraft {
  draft_id: string
  updated_at: string
}

const LEGACY_STORAGE_KEY = "slate.quiz-drafts"
const STORAGE_PREFIX = "slate.quiz-drafts"
const MIGRATION_KEY = "slate.quiz-drafts.migrated"
const MAX_DRAFTS = 100
const SERVER_SNAPSHOT: SavedQuizDraft[] = []

const cache = new Map<string, SavedQuizDraft[]>()
const listeners = new Set<() => void>()

function storageKey(userId: string): string {
  return `${STORAGE_PREFIX}.${encodeURIComponent(userId)}`
}

function currentUserId(): string | null {
  return getAuth().user?.user_id ?? null
}

function isDraft(value: unknown): value is SavedQuizDraft {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<SavedQuizDraft>
  return (
    nonEmpty(candidate.draft_id) &&
    typeof candidate.title === "string" &&
    validDate(candidate.updated_at) &&
    Array.isArray(candidate.questions) &&
    candidate.questions.every(
      (question) =>
        isRecord(question) &&
        typeof question.question === "string" &&
        Array.isArray(question.options) &&
        question.options.every((option) => typeof option === "string") &&
        nonNegativeInteger(question.answer) &&
        (question.points === undefined ||
          (typeof question.points === "number" &&
            Number.isFinite(question.points))) &&
        (question.feedback === undefined ||
          typeof question.feedback === "string")
    ) &&
    [
      candidate.duration_minutes,
      candidate.max_attempts,
      candidate.late_grace_minutes,
    ].every(
      (setting) =>
        setting === undefined ||
        (typeof setting === "number" && Number.isFinite(setting))
    ) &&
    [
      candidate.opens_at,
      candidate.closes_at,
      candidate.classroom_id,
      candidate.instructions,
      candidate.id,
    ].every(
      (setting) => setting === undefined || typeof setting === "string"
    ) &&
    (candidate.allow_late === undefined ||
      typeof candidate.allow_late === "boolean")
  )
}

function parseDrafts(raw: string | null): SavedQuizDraft[] {
  try {
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed)
      ? uniqueBy(parsed.filter(isDraft), (draft) => draft.draft_id).slice(
          0,
          MAX_DRAFTS
        )
      : []
  } catch {
    return []
  }
}

function migrateLegacy(userId: string, key: string): void {
  if (typeof window === "undefined" || !userId) return
  try {
    if (window.localStorage.getItem(key) !== null) return
    if (window.localStorage.getItem(MIGRATION_KEY) === "complete") return
    const legacy = window.localStorage.getItem(LEGACY_STORAGE_KEY)
    if (legacy !== null) {
      const migrated = parseDrafts(legacy)
      if (migrated.length > 0)
        window.localStorage.setItem(key, JSON.stringify(migrated))
      window.localStorage.removeItem(LEGACY_STORAGE_KEY)
    }
    window.localStorage.setItem(MIGRATION_KEY, "complete")
  } catch {
    // Keep legacy data intact when storage is unavailable.
  }
}

function read(userId: string): SavedQuizDraft[] {
  if (typeof window === "undefined" || !userId) return []
  const key = storageKey(userId)
  migrateLegacy(userId, key)
  try {
    return parseDrafts(window.localStorage.getItem(key))
  } catch {
    return []
  }
}

function getSnapshot(userId: string | null): SavedQuizDraft[] {
  if (!userId) return SERVER_SNAPSHOT
  let snapshot = cache.get(userId)
  if (!snapshot) {
    snapshot = read(userId)
    cache.set(userId, snapshot)
  }
  return snapshot
}

function getServerSnapshot(): SavedQuizDraft[] {
  return SERVER_SNAPSHOT
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function notify(): void {
  for (const listener of listeners) listener()
}

function persist(userId: string | null, next: SavedQuizDraft[]): void {
  if (!userId) return
  const limited = next.slice(0, MAX_DRAFTS)
  cache.set(userId, limited)
  try {
    window.localStorage.setItem(storageKey(userId), JSON.stringify(limited))
  } catch {
    // A draft still remains usable for the current session.
  }
  notify()
}

export function saveQuizDraft(
  draft: ExamDraft,
  draftId?: string
): SavedQuizDraft {
  const userId = currentUserId()
  const saved: SavedQuizDraft = {
    ...draft,
    draft_id: draftId ?? localId("draft"),
    updated_at: new Date().toISOString(),
    questions: draft.questions.map((question) => ({
      ...question,
      options: [...question.options],
    })),
  }
  persist(userId, [
    saved,
    ...getSnapshot(userId).filter((item) => item.draft_id !== saved.draft_id),
  ])
  return saved
}

export function removeQuizDraft(draftId: string): void {
  const userId = currentUserId()
  persist(
    userId,
    getSnapshot(userId).filter((draft) => draft.draft_id !== draftId)
  )
}

export function useQuizDrafts(): SavedQuizDraft[] {
  const { user } = useAuth()
  const userId = user?.user_id ?? null
  return useSyncExternalStore(
    subscribe,
    () => getSnapshot(userId),
    getServerSnapshot
  )
}
