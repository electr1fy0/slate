import { useCallback, useEffect, useRef, useState } from "react"

import { boardSocketUrl } from "@/lib/api"
import { getAccessToken } from "@/lib/auth"
import type { BoardElement, ClientMessage, PeerInfo, Role, ServerMessage } from "@/lib/types"

export type ConnectionStatus = "connecting" | "live" | "reconnecting"

/** Maximum number of element batches buffered while offline. */
const OUTBOX_CAP = 200
/** App-level heartbeat cadence; server evicts peers silent for 90s+. */
const HEARTBEAT_INTERVAL_MS = 20_000

interface UseBoardSyncOptions {
  roomId: string
  name: string
  role: Role
  onInit: (elements: BoardElement[]) => void
  onElements: (elements: BoardElement[]) => void
}

function drainOutbox(outbox: ClientMessage[]): ClientMessage[] {
  return outbox.splice(0)
}

export function useBoardSync(options: UseBoardSyncOptions) {
  const [status, setStatus] = useState<ConnectionStatus>("connecting")
  const [peers, setPeers] = useState<PeerInfo[]>([])
  const [selfId, setSelfId] = useState<string | null>(null)

  const socketRef = useRef<WebSocket | null>(null)
  const attemptRef = useRef(0)
  const outboxRef = useRef<ClientMessage[]>([])
  const heartbeatRef = useRef<number | null>(null)
  const optionsRef = useRef(options)
  optionsRef.current = options
  const reconnectRef = useRef<() => void>(() => {})

  const send = useCallback((message: ClientMessage) => {
    const socket = socketRef.current
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(message))
      return
    }
    // Offline: buffer drawing work so it survives a reconnect; stale cursor
    // positions are dropped instead of queued.
    if (message.type === "elements") {
      const outbox = outboxRef.current
      outbox.push(message)
      if (outbox.length > OUTBOX_CAP) outbox.splice(0, outbox.length - OUTBOX_CAP)
    }
  }, [])

  useEffect(() => {
    let disposed = false
    let reconnectTimer: number | undefined

    function connect() {
      setStatus(attemptRef.current === 0 ? "connecting" : "reconnecting")
      // Read the token fresh on every attempt so a mid-session refresh
      // is picked up by the next reconnect.
      const socket = new WebSocket(boardSocketUrl(optionsRef.current.roomId, getAccessToken()))
      socketRef.current = socket

      socket.onopen = () => {
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
              current.send(JSON.stringify({ type: "ping" }))
            }
          }, HEARTBEAT_INTERVAL_MS)
        }
      }

      socket.onmessage = (event) => {
        let message: ServerMessage
        try {
          message = JSON.parse(event.data as string) as ServerMessage
        } catch {
          return
        }
        switch (message.type) {
          case "init":
            setSelfId(message.you)
            setPeers(message.peers)
            optionsRef.current.onInit(message.elements)
            break
          case "elements":
            optionsRef.current.onElements(message.elements)
            break
          case "peer-joined":
            setPeers((current) => [
              ...current.filter((peer) => peer.id !== message.peer.id),
              message.peer,
            ])
            break
          case "peer-left":
            setPeers((current) => current.filter((peer) => peer.id !== message.peer.id))
            break
          case "cursor":
            setPeers((current) =>
              current.map((peer) =>
                peer.id === message.peer.id ? { ...message.peer } : peer,
              ),
            )
            break
          case "pong":
          case "error":
            // Heartbeat replies and protocol errors need no UI action here;
            // the status badge already reflects transport health.
            break
        }
      }

      socket.onclose = () => {
        if (disposed) return
        const delay = Math.min(8000, 500 * 2 ** attemptRef.current)
        attemptRef.current += 1
        reconnectTimer = window.setTimeout(connect, delay)
      }

      socket.onerror = () => socket.close()
    }

    reconnectRef.current = () => {
      if (disposed) return
      const socket = socketRef.current
      if (socket &&
          (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
        return
      }
      if (reconnectTimer !== undefined) {
        window.clearTimeout(reconnectTimer)
        reconnectTimer = undefined
      }
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

    return () => {
      disposed = true
      reconnectRef.current = () => {}
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      if (heartbeatRef.current !== null) {
        window.clearInterval(heartbeatRef.current)
        heartbeatRef.current = null
      }
      const socket = socketRef.current
      if (socket) {
        socket.onclose = null
        socket.onerror = null
        socket.close()
      }
      setPeers([])
      setSelfId(null)
    }
  }, [options.roomId, send])

  const identity = `${options.name}|${options.role}`
  useEffect(() => {
    if (status === "live") {
      const [name, role] = identity.split("|")
      send({ type: "join", name, role: role as Role })
    }
  }, [identity, status, send])

  const reconnect = useCallback(() => {
    reconnectRef.current()
  }, [])

  return { status, peers, selfId, send, reconnect }
}
