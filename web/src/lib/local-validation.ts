/** Contracts for data restored from browser storage; TypeScript cannot validate JSON. */
export function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

export function validDate(value: unknown): value is string {
  return (
    typeof value === "string" &&
    !!value.trim() &&
    Number.isFinite(Date.parse(value))
  )
}

export function nonEmpty(value: unknown): value is string {
  return typeof value === "string" && !!value.trim()
}

export function nonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
}

export function answerMap(value: unknown): value is Record<string, number> {
  return (
    isRecord(value) &&
    Object.entries(value).every(
      ([key, answer]) =>
        /^(0|[1-9]\d*)$/.test(key) &&
        Number.isSafeInteger(Number(key)) &&
        nonNegativeInteger(answer)
    )
  )
}

export function uniqueBy<T>(items: T[], identity: (item: T) => string): T[] {
  const seen = new Set<string>()
  return items.filter((item) => {
    const id = identity(item)
    if (seen.has(id)) return false
    seen.add(id)
    return true
  })
}

let localSequence = 0
export function localId(prefix: string): string {
  return `${prefix}-${typeof crypto !== "undefined" && typeof crypto.randomUUID === "function" ? crypto.randomUUID() : `${Date.now().toString(36)}-${++localSequence}-${Math.random().toString(36).slice(2)}`}`
}
