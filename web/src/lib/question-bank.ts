import { useSyncExternalStore } from "react"

import { getAuth, useAuth } from "./auth"
import type { QuestionDraft } from "./types"

export interface QuestionBankItem extends QuestionDraft {
  id: string
  saved_at: string
}

const LEGACY_STORAGE_KEY = "slate.question-bank"
const STORAGE_PREFIX = "slate.question-bank"
const MIGRATION_KEY = "slate.question-bank.migrated"
const MAX_ITEMS = 500
const SERVER_SNAPSHOT: QuestionBankItem[] = []

const cache = new Map<string, QuestionBankItem[]>()
const listeners = new Set<() => void>()

function storageKey(userId: string): string {
  return `${STORAGE_PREFIX}.${encodeURIComponent(userId)}`
}

function currentUserId(): string | null {
  return getAuth().user?.user_id ?? null
}

function isQuestionBankItem(value: unknown): value is QuestionBankItem {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<QuestionBankItem>
  return (
    typeof candidate.id === "string" &&
    typeof candidate.saved_at === "string" &&
    typeof candidate.question === "string" &&
    Array.isArray(candidate.options) &&
    candidate.options.every((option) => typeof option === "string") &&
    typeof candidate.answer === "number"
  )
}

function parseItems(raw: string | null): QuestionBankItem[] {
  try {
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed)
      ? parsed.filter(isQuestionBankItem).slice(0, MAX_ITEMS)
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
      const migrated = parseItems(legacy)
      if (migrated.length > 0)
        window.localStorage.setItem(key, JSON.stringify(migrated))
      window.localStorage.removeItem(LEGACY_STORAGE_KEY)
    }
    window.localStorage.setItem(MIGRATION_KEY, "complete")
  } catch {
    // Leave the legacy value available if browser storage is blocked.
  }
}

function read(userId: string): QuestionBankItem[] {
  if (typeof window === "undefined" || !userId) return []
  const key = storageKey(userId)
  migrateLegacy(userId, key)
  try {
    return parseItems(window.localStorage.getItem(key))
  } catch {
    return []
  }
}

function getSnapshot(userId: string | null): QuestionBankItem[] {
  if (!userId) return SERVER_SNAPSHOT
  let snapshot = cache.get(userId)
  if (!snapshot) {
    snapshot = read(userId)
    cache.set(userId, snapshot)
  }
  return snapshot
}

function notify(): void {
  for (const listener of listeners) listener()
}

function persist(userId: string | null, next: QuestionBankItem[]): void {
  if (!userId) return
  const limited = next.slice(0, MAX_ITEMS)
  cache.set(userId, limited)
  try {
    window.localStorage.setItem(storageKey(userId), JSON.stringify(limited))
  } catch {
    // Keep the bank usable for this session if storage is unavailable.
  }
  notify()
}

export function saveQuestionToBank(question: QuestionDraft): QuestionBankItem {
  const userId = currentUserId()
  const item: QuestionBankItem = {
    ...question,
    id: `question-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`,
    saved_at: new Date().toISOString(),
    options: [...question.options],
  }
  persist(userId, [item, ...getSnapshot(userId)])
  return item
}

export function removeQuestionFromBank(id: string): void {
  const userId = currentUserId()
  persist(
    userId,
    getSnapshot(userId).filter((item) => item.id !== id)
  )
}

export function useQuestionBank(): QuestionBankItem[] {
  const { user } = useAuth()
  const userId = user?.user_id ?? null
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => getSnapshot(userId),
    () => SERVER_SNAPSHOT
  )
}
