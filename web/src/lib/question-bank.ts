import { useSyncExternalStore } from "react"

import type { QuestionDraft } from "./types"

export interface QuestionBankItem extends QuestionDraft {
  id: string
  saved_at: string
}

const STORAGE_KEY = "slate.question-bank"
const MAX_ITEMS = 100
const SERVER_SNAPSHOT: QuestionBankItem[] = []

let cache: QuestionBankItem[] | null = null
const listeners = new Set<() => void>()

function getSnapshot(): QuestionBankItem[] {
  if (cache) return cache
  if (typeof window === "undefined") return []
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "[]")
    cache = Array.isArray(parsed) ? parsed.filter(isQuestionBankItem).slice(0, MAX_ITEMS) : []
  } catch {
    cache = []
  }
  return cache
}

function getServerSnapshot(): QuestionBankItem[] {
  return SERVER_SNAPSHOT
}

function isQuestionBankItem(value: unknown): value is QuestionBankItem {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<QuestionBankItem>
  return (
    typeof candidate.id === "string" &&
    typeof candidate.saved_at === "string" &&
    typeof candidate.question === "string" &&
    Array.isArray(candidate.options) &&
    typeof candidate.answer === "number"
  )
}

function notify(): void {
  for (const listener of listeners) listener()
}

function persist(next: QuestionBankItem[]): void {
  cache = next.slice(0, MAX_ITEMS)
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(cache))
  } catch {
    // Keep the bank usable for this session if storage is unavailable.
  }
  notify()
}

export function saveQuestionToBank(question: QuestionDraft): QuestionBankItem {
  const item: QuestionBankItem = {
    ...question,
    id: `question-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`,
    saved_at: new Date().toISOString(),
    options: [...question.options],
  }
  persist([item, ...getSnapshot()])
  return item
}

export function removeQuestionFromBank(id: string): void {
  persist(getSnapshot().filter((item) => item.id !== id))
}

export function useQuestionBank(): QuestionBankItem[] {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    getSnapshot,
    getServerSnapshot,
  )
}
