import { useSyncExternalStore } from "react"

const STORAGE_KEY = "slate.admin-token"
let cache: string | undefined
let ephemeral = false
let listening = false
const listeners = new Set<() => void>()
function notify() {
  for (const listener of listeners) listener()
}

export function readAdminToken(): string {
  if (!listening && typeof window !== "undefined") {
    listening = true
    window.addEventListener("storage", (event) => {
      try {
        if (event.storageArea && event.storageArea !== window.localStorage) return
      } catch { return }
      if (event.key !== STORAGE_KEY && event.key !== null) return
      cache = undefined
      ephemeral = false
      notify()
    })
  }
  if (ephemeral) return cache ?? ""
  try {
    cache = window.localStorage.getItem(STORAGE_KEY) ?? ""
  } catch {
    cache ??= ""
  }
  return cache
}

export function storeAdminToken(token: string): void {
  cache = token
  try {
    if (token) window.localStorage.setItem(STORAGE_KEY, token)
    else window.localStorage.removeItem(STORAGE_KEY)
    ephemeral = false
  } catch {
    ephemeral = true
    // storage unavailable — keep token ephemeral
  }
  notify()
}

export function useAdminToken(): [string, (token: string) => void] {
  const token = useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    readAdminToken,
    () => ""
  )
  return [token, storeAdminToken]
}
