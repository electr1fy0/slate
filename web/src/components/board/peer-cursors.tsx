import type { PeerInfo } from "@/lib/types"

const CURSOR_COLORS = [
  "#0369a1",
  "#6d28d9",
  "#92400e",
  "#047857",
  "#b91c1c",
  "#be185d",
]

function colorForPeer(peerId: string): string {
  let hash = 0
  for (const char of peerId) {
    hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  }
  return CURSOR_COLORS[hash % CURSOR_COLORS.length]
}

interface PeerCursorsProps {
  peers: PeerInfo[]
  selfId: string | null
}

/** Absolutely-positioned overlay showing where remote peers are pointing. */
export function PeerCursors({ peers, selfId }: PeerCursorsProps) {
  const visible = peers.filter(
    (peer) =>
      peer.id !== selfId &&
      peer.cursor &&
      peer.cursor.x !== null &&
      peer.cursor.y !== null
  )
  if (visible.length === 0) return null
  return (
    <div
      className="pointer-events-none absolute inset-0 z-10 overflow-hidden"
      aria-hidden="true"
    >
      {visible.map((peer) => {
        const color = colorForPeer(peer.id)
        return (
          <div
            key={peer.id}
            className="absolute flex items-center gap-0.5 transition-[left,top] duration-100 ease-linear"
            style={{ left: `${peer.cursor!.x}%`, top: `${peer.cursor!.y}%` }}
          >
            <svg
              viewBox="0 0 24 24"
              className="size-4 drop-shadow-sm"
              style={{ color }}
              fill="currentColor"
              stroke="white"
              strokeWidth="1.5"
            >
              <path d="M5 3l14 7-6.5 1.5L9 18z" />
            </svg>
            <span
              className="max-w-40 truncate rounded-full px-1.5 py-0.5 text-[10px] leading-none font-medium text-white shadow"
              style={{ backgroundColor: color }}
            >
              {peer.name}
            </span>
          </div>
        )
      })}
    </div>
  )
}
