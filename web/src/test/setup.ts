import { afterEach } from "vitest"
import { cleanup } from "@testing-library/react"
import "@testing-library/jest-dom/vitest"

// Node >=22 exposes an experimental localStorage that requires
// --localstorage-file; when absent (the default), install a tiny in-memory
// stand-in so code under test can use the Web Storage API unchanged.
function ensureLocalStorage(owner: Record<string, unknown>): void {
  if ("localStorage" in owner && owner.localStorage) return
  const store = new Map<string, string>()
  const storage: Storage = {
    get length() {
      return store.size
    },
    clear: () => store.clear(),
    getItem: (key) => store.get(key) ?? null,
    key: (index) => Array.from(store.keys())[index] ?? null,
    removeItem: (key) => void store.delete(key),
    setItem: (key, value) => void store.set(String(key), String(value)),
  }
  Object.defineProperty(owner, "localStorage", { value: storage, configurable: true })
}

ensureLocalStorage(globalThis)
if (typeof window !== "undefined") ensureLocalStorage(window as unknown as Record<string, unknown>)

afterEach(() => {
  cleanup()
  window.localStorage.clear()
})
