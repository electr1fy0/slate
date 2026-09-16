import { useSyncExternalStore } from "react"

const STORAGE_KEY = "slate.recent-rooms"
const MAX_ROOMS = 8
const SERVER_SNAPSHOT: string[] = []

let cached: string[] | null = null
const listeners = new Set<() => void>()

function read(): string[] {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((room): room is string => typeof room === "string").slice(0, MAX_ROOMS)
  } catch {
    return []
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function getSnapshot(): string[] {
  cached ??= read()
  return cached
}

function getServerSnapshot(): string[] {
  return SERVER_SNAPSHOT
}

export function rememberRoom(roomId: string): void {
  const next = [roomId, ...getSnapshot().filter((room) => room !== roomId)].slice(0, MAX_ROOMS)
  cached = next
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  } catch {
    // storage unavailable — keep list in memory only
  }
  for (const listener of listeners) listener()
}

export function forgetRoom(roomId: string): void {
  const next = getSnapshot().filter((room) => room !== roomId)
  cached = next
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  } catch {
    // storage unavailable — keep list in memory only
  }
  for (const listener of listeners) listener()
}

export function useRecentRooms(): string[] {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
