import { useSyncExternalStore } from "react"

import type { ExamDraft } from "./types"

export interface SavedQuizDraft extends ExamDraft {
  draft_id: string
  updated_at: string
}

const STORAGE_KEY = "slate.quiz-drafts"
const MAX_DRAFTS = 12
const SERVER_SNAPSHOT: SavedQuizDraft[] = []

let cache: SavedQuizDraft[] | null = null
const listeners = new Set<() => void>()

function read(): SavedQuizDraft[] {
  if (typeof window === "undefined") return []
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(isDraft).slice(0, MAX_DRAFTS)
  } catch {
    return []
  }
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

function getSnapshot(): SavedQuizDraft[] {
  cache ??= read()
  return cache
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

function persist(next: SavedQuizDraft[]): void {
  cache = next.slice(0, MAX_DRAFTS)
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(cache))
  } catch {
    // A draft still remains usable for the current session.
  }
  notify()
}

export function saveQuizDraft(draft: ExamDraft, draftId?: string): SavedQuizDraft {
  const saved: SavedQuizDraft = {
    ...draft,
    draft_id: draftId ?? `draft-${Date.now().toString(36)}`,
    updated_at: new Date().toISOString(),
    questions: draft.questions.map((question) => ({
      question: question.question,
      options: [...question.options],
      answer: question.answer,
    })),
  }
  persist([saved, ...getSnapshot().filter((item) => item.draft_id !== saved.draft_id)])
  return saved
}

export function removeQuizDraft(draftId: string): void {
  persist(getSnapshot().filter((draft) => draft.draft_id !== draftId))
}

export function useQuizDrafts(): SavedQuizDraft[] {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
