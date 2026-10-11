import { useSyncExternalStore } from "react"

import { getAuth, useAuth } from "./auth"
import type { ExamDraft } from "./types"

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
    typeof candidate.draft_id === "string" &&
    typeof candidate.title === "string" &&
    typeof candidate.updated_at === "string" &&
    Array.isArray(candidate.questions)
  )
}

function parseDrafts(raw: string | null): SavedQuizDraft[] {
  try {
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed)
      ? parsed.filter(isDraft).slice(0, MAX_DRAFTS)
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
    draft_id: draftId ?? `draft-${Date.now().toString(36)}`,
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
