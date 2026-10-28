import { useCallback, useEffect, useRef, useState } from "react"

import { boardSocketUrl } from "@/lib/api"
import { getAccessToken } from "@/lib/auth"
import type {
  BoardElement,
  ClientMessage,
  PeerInfo,
  Role,
  ServerMessage,
} from "@/lib/types"
import { isRecord } from "@/lib/local-validation"

export type ConnectionStatus =
  "connecting" | "live" | "reconnecting" | "unavailable"

/** Maximum number of element batches buffered while offline. */
const OUTBOX_CAP = 200
/** App-level heartbeat cadence; server evicts peers silent for 90s+. */
const HEARTBEAT_INTERVAL_MS = 20_000

interface UseBoardSyncOptions {
  roomId: string
  sessionId?: string
  name: string
  role: Role
  enabled?: boolean
  onInit: (elements: BoardElement[]) => void
  onElements: (elements: BoardElement[]) => void
  onRestored?: (elements: BoardElement[]) => void
  onPermissions?: (allowed: boolean) => void
  onError?: (message: string) => void
}

function drainOutbox(outbox: ClientMessage[]): ClientMessage[] {
  return outbox.splice(0)
}

export function validBoardMessage(value: unknown): value is ServerMessage {
  if (!isRecord(value)) return false
  const elements = (items: unknown) =>
    Array.isArray(items) &&
    items.every(
      (item) =>
        isRecord(item) && typeof item.id === "string" && item.id.length > 0
    )
  const peer = (item: unknown) =>
    isRecord(item) &&
    typeof item.id === "string" &&
    typeof item.name === "string" &&
    (item.role === "student" || item.role === "teacher")
  switch (value.type) {
    case "init":
      return (
        typeof value.you === "string" &&
        elements(value.elements) &&
        Array.isArray(value.peers) &&
        value.peers.every(peer) &&
        (value.room === undefined ||
          (isRecord(value.room) &&
            typeof value.room.students_draw === "boolean"))
      )
    case "elements":
    case "restored":
      return elements(value.elements)
    case "permissions":
      return typeof value.students_draw === "boolean"
    case "peer-joined":
    case "peer-left":
    case "cursor":
      return peer(value.peer)
    case "pong":
      return true
    case "error":
      return value.message === undefined || typeof value.message === "string"
    default:
      return false
  }
}

export function boardMessageBatches(message: ClientMessage): ClientMessage[] {
  if (message.type !== "elements") return [message]
  const batches: ClientMessage[] = []
  let batch: BoardElement[] = []
  let size = 32
  const encoder = new TextEncoder()
  for (const element of message.elements) {
    const bytes = encoder.encode(JSON.stringify(element)).length + 1
    if (bytes > 60_000)
      throw new Error(
        "A board element is too large to sync. Shorten its content and try again."
      )
    if (batch.length && size + bytes > 60_000) {
      batches.push({ type: "elements", elements: batch })
      batch = []
      size = 32
    }
    batch.push(element)
    size += bytes
  }
  if (batch.length) batches.push({ type: "elements", elements: batch })
  return batches
}

export function useBoardSync(options: UseBoardSyncOptions) {
  const [status, setStatus] = useState<ConnectionStatus>(
    options.enabled === false ? "unavailable" : "connecting"
  )
  const [peers, setPeers] = useState<PeerInfo[]>([])
  const [selfId, setSelfId] = useState<string | null>(null)

  const socketRef = useRef<WebSocket | null>(null)
  const attemptRef = useRef(0)
  const outboxRef = useRef<ClientMessage[]>([])
  const heartbeatRef = useRef<number | null>(null)
  const optionsRef = useRef(options)
  optionsRef.current = options
  const reconnectRef = useRef<(() => void) | null>(null)

  const send = useCallback((message: ClientMessage) => {
    if (optionsRef.current.enabled === false) return
    const socket = socketRef.current
    if (socket && socket.readyState === WebSocket.OPEN) {
      let batches: ClientMessage[]
      try {
        batches = boardMessageBatches(message)
      } catch (error) {
        optionsRef.current.onError?.(
          error instanceof Error
            ? error.message
            : "Could not encode board changes"
        )
        return
      }
      try {
        for (const batch of batches) socket.send(JSON.stringify(batch))
        return
      } catch (error) {
        optionsRef.current.onError?.(
          error instanceof Error
            ? error.message
            : "Could not send board changes"
        )
        socket.close()
      }
    }
    // Offline: buffer drawing work so it survives a reconnect; stale cursor
    // positions are dropped instead of queued.
    if (message.type === "elements") {
      const outbox = outboxRef.current
      outbox.push(message)
      if (outbox.length > OUTBOX_CAP)
        outbox.splice(0, outbox.length - OUTBOX_CAP)
    }
  }, [])

  useEffect(() => {
    if (options.enabled === false) {
      setStatus("unavailable")
      setPeers([])
      setSelfId(null)
      outboxRef.current = []
      return
    }

    attemptRef.current = 0
    outboxRef.current = []
    let disposed = false
    let reconnectTimer: number | undefined

    function connect() {
      if (disposed) return
      setStatus(attemptRef.current === 0 ? "connecting" : "reconnecting")
      // Read the token fresh on every attempt so a mid-session refresh
      // is picked up by the next reconnect.
      let socket: WebSocket
      try {
        socket = new WebSocket(
          boardSocketUrl(optionsRef.current.roomId, getAccessToken())
        )
      } catch {
        setStatus("unavailable")
        optionsRef.current.onError?.(
          "Could not open the board connection. Retry when your network is available."
        )
        return
      }
      socketRef.current = socket

      socket.onopen = () => {
        if (disposed || socketRef.current !== socket) return
        attemptRef.current = 0
        setStatus("live")
        send({
          type: "join",
          name: optionsRef.current.name || "Guest",
          role: optionsRef.current.role,
        })
        for (const message of drainOutbox(outboxRef.current)) {
          send(message)
        }
        if (heartbeatRef.current === null) {
          heartbeatRef.current = window.setInterval(() => {
            const current = socketRef.current
            if (current && current.readyState === WebSocket.OPEN) {
              try {
                current.send(JSON.stringify({ type: "ping" }))
              } catch {
                current.close()
              }
            }
          }, HEARTBEAT_INTERVAL_MS)
        }
      }

      socket.onmessage = (event) => {
        if (disposed || socketRef.current !== socket) return
        let message: ServerMessage
        try {
          message = JSON.parse(event.data as string) as ServerMessage
          if (!validBoardMessage(message)) return
        } catch {
          return
        }
        switch (message.type) {
          case "init":
            setSelfId(message.you)
            setPeers(message.peers)
            optionsRef.current.onInit(message.elements)
            optionsRef.current.onPermissions?.(
              message.room?.students_draw ?? false
            )
            break
          case "elements":
            optionsRef.current.onElements(message.elements)
            break
          case "restored":
            outboxRef.current = []
            optionsRef.current.onRestored?.(message.elements)
            break
          case "permissions":
            optionsRef.current.onPermissions?.(message.students_draw)
            if (!message.students_draw && optionsRef.current.role === "student")
              outboxRef.current = []
            break
          case "peer-joined":
            setPeers((current) => [
              ...current.filter((peer) => peer.id !== message.peer.id),
              message.peer,
            ])
            break
          case "peer-left":
            setPeers((current) =>
              current.filter((peer) => peer.id !== message.peer.id)
            )
            break
          case "cursor":
            setPeers((current) =>
              current.map((peer) =>
                peer.id === message.peer.id ? { ...message.peer } : peer
              )
            )
            break
          case "pong":
            break
          case "error":
            optionsRef.current.onError?.(
              message.message ?? "Board request failed"
            )
            break
        }
      }

      socket.onclose = () => {
        if (disposed || socketRef.current !== socket) return
        setStatus("reconnecting")
        setPeers([])
        setSelfId(null)
        const delay = Math.min(8000, 500 * 2 ** attemptRef.current)
        attemptRef.current += 1
        reconnectTimer = window.setTimeout(connect, delay)
      }

      socket.onerror = () => socket.close()
    }

    reconnectRef.current = () => {
      const socket = socketRef.current
      if (
        socket &&
        (socket.readyState === WebSocket.OPEN ||
          socket.readyState === WebSocket.CONNECTING)
      ) {
        return
      }
      if (reconnectTimer !== undefined) {
        window.clearTimeout(reconnectTimer)
        reconnectTimer = undefined
      }
      /* c8 ignore next */
      if (socket) {
        socket.onclose = null
        socket.onerror = null
        socket.close()
      }
      attemptRef.current = 0
      setStatus("connecting")
      connect()
    }

    connect()

    const onOnline = () => reconnectRef.current?.()
    window.addEventListener("online", onOnline)

    return () => {
      disposed = true
      window.removeEventListener("online", onOnline)
      reconnectRef.current = null
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      if (heartbeatRef.current !== null) {
        window.clearInterval(heartbeatRef.current)
        heartbeatRef.current = null
      }
      const socket = socketRef.current
      /* c8 ignore next */
      if (socket) {
        socket.onclose = null
        socket.onerror = null
        socket.close()
      }
      setPeers([])
      setSelfId(null)
    }
  }, [options.enabled, options.roomId, options.sessionId, send])

  const identity = `${options.name}|${options.role}`
  useEffect(() => {
    if (status === "live") {
      const [name, role] = identity.split("|")
      send({ type: "join", name, role: role as Role })
    }
  }, [identity, status, send])

  const reconnect = useCallback(() => {
    reconnectRef.current?.()
  }, [])

  return { status, peers, selfId, send, reconnect }
}
