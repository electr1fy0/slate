import { useSyncExternalStore } from "react"
import {
  nonEmpty,
  nonNegativeInteger,
  uniqueBy,
  validDate,
} from "./local-validation"

export interface BoardHistoryEntry {
  room_id: string
  first_opened_at: string
  last_opened_at: string
  visits: number
  source: "local-history"
}

const STORAGE_KEY = "slate.board-history"
const MAX_ENTRIES = 20
const SERVER_SNAPSHOT: BoardHistoryEntry[] = []

let cache: BoardHistoryEntry[] | null = null
let listening = false
const listeners = new Set<() => void>()

function isEntry(value: unknown): value is BoardHistoryEntry {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<BoardHistoryEntry>
  return (
    nonEmpty(candidate.room_id) &&
    validDate(candidate.first_opened_at) &&
    validDate(candidate.last_opened_at) &&
    Date.parse(candidate.first_opened_at) <=
      Date.parse(candidate.last_opened_at) &&
    nonNegativeInteger(candidate.visits) &&
    candidate.visits > 0 &&
    candidate.source === "local-history"
  )
}

function read(): BoardHistoryEntry[] {
  if (typeof window === "undefined") return []
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed)
      ? uniqueBy(
          parsed
            .filter(isEntry)
            .sort(
              (a, b) =>
                Date.parse(b.last_opened_at) - Date.parse(a.last_opened_at)
            ),
          (entry) => entry.room_id
        ).slice(0, MAX_ENTRIES)
      : []
  } catch {
    return []
  }
}

function getSnapshot(): BoardHistoryEntry[] {
  if (!listening && typeof window !== "undefined") {
    listening = true
    window.addEventListener("storage", (event) => {
      if (event.key !== STORAGE_KEY && event.key !== null) return
      cache = null
      notify()
    })
  }
  cache ??= read()
  return cache
}

function getServerSnapshot(): BoardHistoryEntry[] {
  return SERVER_SNAPSHOT
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function notify(): void {
  for (const listener of listeners) listener()
}

function persist(next: BoardHistoryEntry[]): void {
  cache = next.slice(0, MAX_ENTRIES)
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(cache))
  } catch {
    // History remains available in-memory if local storage is unavailable.
  }
  notify()
}

export function recordBoardVisit(roomId: string): void {
  if (!roomId.trim()) return
  const now = new Date().toISOString()
  const previous = getSnapshot().find((entry) => entry.room_id === roomId)
  const next: BoardHistoryEntry = previous
    ? {
        ...previous,
        last_opened_at: now,
        visits: Math.min(Number.MAX_SAFE_INTEGER, previous.visits + 1),
      }
    : {
        room_id: roomId,
        first_opened_at: now,
        last_opened_at: now,
        visits: 1,
        source: "local-history",
      }
  persist([next, ...getSnapshot().filter((entry) => entry.room_id !== roomId)])
}

export function forgetBoardHistory(roomId: string): void {
  persist(getSnapshot().filter((entry) => entry.room_id !== roomId))
}

export function useBoardHistory(): BoardHistoryEntry[] {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}

export function mergeBoardHistory(
  recentRooms: string[],
  history: BoardHistoryEntry[]
): BoardHistoryEntry[] {
  const known = new Map(history.map((entry) => [entry.room_id, entry]))
  const fallbackTime = new Date(0).toISOString()
  for (const roomId of recentRooms) {
    if (!known.has(roomId)) {
      known.set(roomId, {
        room_id: roomId,
        first_opened_at: fallbackTime,
        last_opened_at: fallbackTime,
        visits: 1,
        source: "local-history",
      })
    }
  }
  return [...known.values()]
    .filter(isEntry)
    .sort(
      (a, b) =>
        Date.parse(b.last_opened_at) - Date.parse(a.last_opened_at) ||
        a.room_id.localeCompare(b.room_id)
    )
}

export function boardHistoryCsv(entries: BoardHistoryEntry[]): string {
  const escape = (value: string): string => `"${value.replace(/"/g, '""')}"`
  const rows = [
    ["room_id", "last_opened_at", "visits", "source"],
    ...entries.map((entry) => [
      entry.room_id,
      entry.last_opened_at,
      String(entry.visits),
      entry.source,
    ]),
  ]
  return rows.map((row) => row.map(escape).join(",")).join("\n")
}

export function downloadTextFile(
  filename: string,
  contents: string,
  type: string
): void {
  const blob = new Blob([contents], { type })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = filename
  anchor.hidden = true
  document.body.appendChild(anchor)
  try {
    anchor.click()
  } finally {
    anchor.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
}
