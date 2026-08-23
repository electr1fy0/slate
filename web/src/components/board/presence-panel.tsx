import { UsersIcon } from "lucide-react"

import type { PeerInfo } from "@/lib/types"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Separator } from "@/components/ui/separator"

function initialsOf(name: string): string {
  const trimmed = name.trim()
  return trimmed ? trimmed.slice(0, 2).toUpperCase() : "??"
}

interface PresencePanelProps {
  peers: PeerInfo[]
  selfName: string
  selfRole: "teacher" | "student"
}

/** Header control revealing the full participant list with roles. */
export function PresencePanel({ peers, selfName, selfRole }: PresencePanelProps) {
  const total = peers.length + 1
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button variant="outline" size="sm" aria-label={`Participants (${total})`}>
            <UsersIcon data-icon="inline-start" />
            {total}
          </Button>
        }
      />
      <PopoverContent align="end" className="w-56">
        <p className="mb-1.5 text-xs font-medium text-muted-foreground">
          In this room
        </p>
        <ul className="flex flex-col gap-1.5">
          <li className="flex items-center gap-2">
            <Avatar className="size-6">
              <AvatarFallback className="text-[10px]">{initialsOf(selfName)}</AvatarFallback>
            </Avatar>
            <span className="min-w-0 flex-1 truncate text-sm">{selfName || "You"}</span>
            <Badge variant={selfRole === "teacher" ? "default" : "secondary"}>
              {selfRole === "teacher" ? "Staff" : "Learner"}
            </Badge>
          </li>
          {peers.length > 0 ? <Separator /> : null}
          {peers.map((peer) => (
            <li key={peer.id} className="flex items-center gap-2">
              <Avatar className="size-6">
                <AvatarFallback className="text-[10px]">
                  {initialsOf(peer.name)}
                </AvatarFallback>
              </Avatar>
              <span className="min-w-0 flex-1 truncate text-sm">{peer.name}</span>
              <Badge variant={peer.role === "teacher" ? "default" : "secondary"}>
                {peer.role === "teacher" ? "Staff" : "Learner"}
              </Badge>
            </li>
          ))}
        </ul>
      </PopoverContent>
    </Popover>
  )
}
