import { isRecord } from "./local-validation"
import type { BoardElement } from "./types"

export function validBoardElement(value: unknown): value is BoardElement {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    !!value.id.trim() &&
    (value.version === undefined ||
      (Number.isSafeInteger(value.version) && Number(value.version) >= 0))
  )
}

export function mergeBoardElements(
  current: BoardElement[],
  incoming: BoardElement[]
): BoardElement[] {
  const elements = new Map<string, BoardElement>()
  for (const element of [...current, ...incoming]) {
    if (!validBoardElement(element)) continue
    const previous = elements.get(element.id)
    if (
      !previous ||
      (element.version ?? 0) > (previous.version ?? 0) ||
      ((element.version ?? 0) === (previous.version ?? 0) &&
        typeof element.versionNonce === "number" &&
        typeof previous.versionNonce === "number" &&
        element.versionNonce < previous.versionNonce)
    )
      elements.set(element.id, element)
  }
  return [...elements.values()]
}
