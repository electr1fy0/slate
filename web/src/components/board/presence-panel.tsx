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

function initialsOf(name: string): string {
  const trimmed = name.trim()
  return trimmed ? Array.from(trimmed).slice(0, 2).join("").toUpperCase() : "??"
}

interface PresencePanelProps {
  peers: PeerInfo[]
  selfName: string
  selfRole: "teacher" | "student"
}

/** Header control revealing the full participant list with roles. */
export function PresencePanel({
  peers,
  selfName,
  selfRole,
}: PresencePanelProps) {
  const total = peers.length + 1
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            variant="outline"
            size="sm"
            aria-label={`Participants (${total})`}
          >
            <UsersIcon data-icon="inline-start" />
            {total}
          </Button>
        }
      />
      <PopoverContent
        align="end"
        className="max-h-[min(30rem,80vh)] w-56 overflow-y-auto"
      >
        <p className="mb-1.5 text-xs font-medium text-muted-foreground">
          In this room
        </p>
        <ul className="flex flex-col gap-1.5">
          <li className="flex items-center gap-2">
            <Avatar className="size-6">
              <AvatarFallback className="text-[10px]">
                {initialsOf(selfName)}
              </AvatarFallback>
            </Avatar>
            <span title={selfName} className="min-w-0 flex-1 truncate text-sm">
              {selfName.trim() || "You"}
            </span>
            <Badge variant={selfRole === "teacher" ? "default" : "secondary"}>
              {selfRole === "teacher" ? "Staff" : "Learner"}
            </Badge>
          </li>
          {peers.length > 0 ? (
            <li role="separator" className="h-px bg-border" />
          ) : null}
          {peers.map((peer) => (
            <li key={peer.id} className="flex items-center gap-2">
              <Avatar className="size-6">
                <AvatarFallback className="text-[10px]">
                  {initialsOf(peer.name)}
                </AvatarFallback>
              </Avatar>
              <span
                title={peer.name}
                className="min-w-0 flex-1 truncate text-sm"
              >
                {peer.name.trim() || "Participant"}
              </span>
              <Badge
                variant={peer.role === "teacher" ? "default" : "secondary"}
              >
                {peer.role === "teacher" ? "Staff" : "Learner"}
              </Badge>
            </li>
          ))}
        </ul>
      </PopoverContent>
    </Popover>
  )
}
