import { useEffect, useState } from "react"

const STORAGE_KEY = "slate.admin-token"

export function readAdminToken(): string {
  try {
    return window.localStorage.getItem(STORAGE_KEY) ?? ""
  } catch {
    return ""
  }
}

export function storeAdminToken(token: string): void {
  try {
    if (token) window.localStorage.setItem(STORAGE_KEY, token)
    else window.localStorage.removeItem(STORAGE_KEY)
  } catch {
    // storage unavailable — keep token ephemeral
  }
}

export function useAdminToken(): [string, (token: string) => void] {
  const [token, setToken] = useState("")

  useEffect(() => setToken(readAdminToken()), [])

  const save = (next: string) => {
    setToken(next)
    storeAdminToken(next)
  }

  return [token, save]
}
