import { useSyncExternalStore } from "react"

export interface AuthUser {
  user_id: string
  email: string
  name: string
  role: "student" | "teacher"
}

export interface AuthState {
  user: AuthUser | null
  accessToken: string | null
  refreshToken: string | null
}

const STORAGE_KEY = "slate.auth"

const EMPTY: AuthState = { user: null, accessToken: null, refreshToken: null }

let cached: AuthState | null = null
const listeners = new Set<() => void>()

function read(): AuthState {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return EMPTY
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== "object") return EMPTY
    const record = parsed as Record<string, unknown>
    let user: AuthUser | null = null
    const rawUser = record.user
    if (rawUser && typeof rawUser === "object") {
      const candidate = rawUser as Record<string, unknown>
      if (typeof candidate.user_id === "string") {
        user = {
          user_id: candidate.user_id,
          email: String(candidate.email ?? ""),
          name: String(candidate.name ?? ""),
          role: candidate.role === "teacher" ? "teacher" : "student",
        }
      }
    }
    return {
      user,
      accessToken: typeof record.accessToken === "string" ? record.accessToken : null,
      refreshToken: typeof record.refreshToken === "string" ? record.refreshToken : null,
    }
  } catch {
    return EMPTY
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function getSnapshot(): AuthState {
  cached ??= read()
  return cached
}

function getServerSnapshot(): AuthState {
  return EMPTY
}

/** Imperative read for non-hook contexts (fetch layer, WebSocket URLs). */
export function getAuth(): AuthState {
  return getSnapshot()
}

export function getAccessToken(): string | null {
  return getSnapshot().accessToken
}

export function getRefreshToken(): string | null {
  return getSnapshot().refreshToken
}

export function isAuthenticated(): boolean {
  return getSnapshot().user !== null
}

function persist(next: AuthState): void {
  cached = next
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  } catch {
    // storage unavailable — session lives in memory only
  }
  for (const listener of listeners) listener()
}

export function setAuth(next: AuthState): void {
  persist(next)
}

export function clearAuth(): void {
  cached = EMPTY
  try {
    // Remove entirely (rather than writing an empty blob) so nothing about
    // the previous session lingers on disk; legacy pre-auth keys go too.
    window.localStorage.removeItem(STORAGE_KEY)
    window.localStorage.removeItem("slate.session")
  } catch {
    // ignore
  }
  for (const listener of listeners) listener()
}

/** React binding; identical snapshot across server render for hydration. */
export function useAuth(): AuthState {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
