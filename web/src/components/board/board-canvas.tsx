import { useCallback, useEffect, useMemo, useRef } from "react"
import {
  Excalidraw,
  CaptureUpdateAction,
  reconcileElements,
} from "@excalidraw/excalidraw"
import type { ExcalidrawImperativeAPI, AppState  } from "@excalidraw/excalidraw/types"
import type { OrderedExcalidrawElement } from "@excalidraw/excalidraw/element/types"
import "@excalidraw/excalidraw/index.css"

import type { BoardElement, ClientMessage } from "@/lib/types"

const FLUSH_INTERVAL_MS = 100
const SAVE_DELAY_MS = 800

export interface BoardCanvasHandle {
  mergeRemote: ((elements: BoardElement[]) => void) | undefined
}

interface BoardCanvasProps {
  roomId: string
  readOnly: boolean
  handle: { current: BoardCanvasHandle }
  senderRef: { current: ((message: ClientMessage) => void) | null }
  onLocalEdit?: () => void
  /** Reports the pointer position as percentages of the visible canvas. */
  onCursorMove?: (xPercent: number, yPercent: number) => void
}

function storageKey(roomId: string): string {
  return `slate.board.${roomId}`
}

function dedupeLatest(elements: OrderedExcalidrawElement[]): OrderedExcalidrawElement[] {
  const latest = new Map<string, OrderedExcalidrawElement>()
  for (const element of elements) latest.set(element.id, element)
  return Array.from(latest.values())
}

function normaliseElement(element: BoardElement): OrderedExcalidrawElement {
  return {
    ...element,
    version: typeof element.version === "number" ? element.version : 0,
  } as OrderedExcalidrawElement
}

function loadSnapshot(roomId: string): OrderedExcalidrawElement[] {
  try {
    const raw = window.localStorage.getItem(storageKey(roomId))
    if (!raw) return []
    const parsed = JSON.parse(raw) as BoardElement[]
    return parsed.filter((element) => element.id && !element.isDeleted) as OrderedExcalidrawElement[]
  } catch {
    return []
  }
}

export default function BoardCanvas({
  roomId,
  readOnly,
  handle,
  senderRef,
  onLocalEdit,
  onCursorMove,
}: BoardCanvasProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null)
  const appStateRef = useRef<AppState | null>(null)
  const elementsRef = useRef<Map<string, OrderedExcalidrawElement>>(new Map())
  const outgoingRef = useRef<OrderedExcalidrawElement[]>([])
  const flushTimerRef = useRef<number | null>(null)
  const saveTimerRef = useRef<number | null>(null)

  const initialElements = useMemo(() => loadSnapshot(roomId), [roomId])
  if (elementsRef.current.size === 0 && initialElements.length > 0) {
    for (const element of initialElements) elementsRef.current.set(element.id, element)
  }

  const scheduleSave = useCallback(() => {
    if (saveTimerRef.current !== null) window.clearTimeout(saveTimerRef.current)
    saveTimerRef.current = window.setTimeout(() => {
      saveTimerRef.current = null
      try {
        const snapshot = Array.from(elementsRef.current.values())
        window.localStorage.setItem(storageKey(roomId), JSON.stringify(snapshot))
      } catch {
        // storage unavailable — autosave stays best effort
      }
    }, SAVE_DELAY_MS)
  }, [roomId])

  const queueOutgoing = useCallback((element: OrderedExcalidrawElement) => {
    outgoingRef.current.push(element)
    if (flushTimerRef.current !== null) return
    flushTimerRef.current = window.setTimeout(() => {
      flushTimerRef.current = null
      const batch = dedupeLatest(outgoingRef.current.splice(0))
      if (batch.length > 0) {
        senderRef.current?.({ type: "elements", elements: batch })
      }
    }, FLUSH_INTERVAL_MS)
  }, [senderRef])

  const handleChange = useCallback(
    (elements: readonly OrderedExcalidrawElement[], appState: AppState) => {
      appStateRef.current = appState
      let changed = false
      for (const element of elements) {
        if (element.isDeleted) continue
        const known = elementsRef.current.get(element.id)
        if (!known || element.version > known.version) {
          elementsRef.current.set(element.id, element)
          changed = true
          queueOutgoing(element)
        }
      }
      if (changed) {
        onLocalEdit?.()
        scheduleSave()
      }
    },
    [onLocalEdit, queueOutgoing, scheduleSave],
  )

  useEffect(() => {
    handle.current.mergeRemote = (incoming: BoardElement[]) => {
      const api = apiRef.current
      const remote = incoming
        .filter((element) => element.id && !element.isDeleted)
        .map(normaliseElement)
      if (!api || remote.length === 0) return

      const local = Array.from(elementsRef.current.values())
      const merged = reconcileElements(
        local,
        remote as never,
        appStateRef.current ?? ({} as AppState),
      )
      elementsRef.current.clear()
      for (const element of merged) {
        if (!element.isDeleted) elementsRef.current.set(element.id, element)
      }
      api.updateScene({ elements: merged, captureUpdate: CaptureUpdateAction.NEVER })
      scheduleSave()
    }
    return () => {
      handle.current.mergeRemote = undefined
    }
  }, [handle, scheduleSave])

  useEffect(() => {
    return () => {
      if (flushTimerRef.current !== null) window.clearTimeout(flushTimerRef.current)
      if (saveTimerRef.current !== null) window.clearTimeout(saveTimerRef.current)
    }
  }, [])

  const cursorCallbackRef = useRef(onCursorMove)
  cursorCallbackRef.current = onCursorMove

  useEffect(() => {
    const container = containerRef.current
    if (!container || !onCursorMove) return
    function handlePointerMove(event: PointerEvent) {
      const callback = cursorCallbackRef.current
      const el = containerRef.current
      if (!callback || !el) return
      const rect = el.getBoundingClientRect()
      if (rect.width <= 0 || rect.height <= 0) return
      const x = Math.min(100, Math.max(0, ((event.clientX - rect.left) / rect.width) * 100))
      const y = Math.min(100, Math.max(0, ((event.clientY - rect.top) / rect.height) * 100))
      callback(Math.round(x * 10) / 10, Math.round(y * 10) / 10)
    }
    container.addEventListener("pointermove", handlePointerMove)
    return () => container.removeEventListener("pointermove", handlePointerMove)
  }, [onCursorMove])

  return (
    <div ref={containerRef} className="absolute inset-0">
      <Excalidraw
        theme="light"
        viewModeEnabled={readOnly}
        initialData={{ elements: initialElements }}
        excalidrawAPI={(api) => {
          apiRef.current = api
        }}
        onChange={handleChange}
      />
    </div>
  )
}
