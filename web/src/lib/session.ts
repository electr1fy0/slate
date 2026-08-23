import { useSyncExternalStore } from "react"
import type { Role } from "./types"

export interface Session {
  role: Role
  name: string
}

const STORAGE_KEY = "slate.session"

const DEFAULT_SESSION: Session = { role: "teacher", name: "" }

let cached: Session | null = null
const listeners = new Set<() => void>()

function readSession(): Session {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT_SESSION
    const parsed = JSON.parse(raw) as Partial<Session>
    return {
      role: parsed.role === "student" ? "student" : "teacher",
      name: typeof parsed.name === "string" ? parsed.name : "",
    }
  } catch {
    return DEFAULT_SESSION
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function getSnapshot(): Session {
  cached ??= readSession()
  return cached
}

function getServerSnapshot(): Session {
  return DEFAULT_SESSION
}

function updateSession(next: Partial<Session>): void {
  const merged = { ...getSnapshot(), ...next }
  cached = merged
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(merged))
  } catch {
    // storage unavailable (private mode) — session stays in memory
  }
  for (const listener of listeners) listener()
}

export function useSession(): [Session, typeof updateSession] {
  const session = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
  return [session, updateSession]
}
