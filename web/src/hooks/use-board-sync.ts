import { useCallback, useEffect, useRef, useState } from "react"

import { boardSocketUrl } from "@/lib/api"
import type { BoardElement, ClientMessage, PeerInfo, Role, ServerMessage } from "@/lib/types"

export type ConnectionStatus = "connecting" | "live" | "reconnecting"

interface UseBoardSyncOptions {
  roomId: string
  name: string
  role: Role
  onInit: (elements: BoardElement[]) => void
  onElements: (elements: BoardElement[]) => void
}

export function useBoardSync(options: UseBoardSyncOptions) {
  const [status, setStatus] = useState<ConnectionStatus>("connecting")
  const [peers, setPeers] = useState<PeerInfo[]>([])
  const [selfId, setSelfId] = useState<string | null>(null)

  const socketRef = useRef<WebSocket | null>(null)
  const attemptRef = useRef(0)
  const optionsRef = useRef(options)
  optionsRef.current = options

  const send = useCallback((message: ClientMessage) => {
    const socket = socketRef.current
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(message))
    }
  }, [])

  useEffect(() => {
    let disposed = false
    let reconnectTimer: number | undefined

    function connect() {
      setStatus(attemptRef.current === 0 ? "connecting" : "reconnecting")
      const socket = new WebSocket(boardSocketUrl(optionsRef.current.roomId))
      socketRef.current = socket

      socket.onopen = () => {
        attemptRef.current = 0
        setStatus("live")
        send({
          type: "join",
          name: optionsRef.current.name || "Guest",
          role: optionsRef.current.role,
        })
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

    connect()

    return () => {
      disposed = true
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
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

  return { status, peers, selfId, send }
}
