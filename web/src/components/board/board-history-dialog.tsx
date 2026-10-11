import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { HistoryIcon } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useAuth } from "@/lib/auth"
import type { BoardElement } from "@/lib/types"
import { ConfirmAction } from "@/components/confirm-action"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"

export function BoardHistoryDialog({
  roomId,
  enabled,
  onRestored,
}: {
  roomId: string
  enabled: boolean
  onRestored: (elements: BoardElement[]) => void
}) {
  const [open, setOpen] = useState(false)
  const auth = useAuth()
  const query = useQuery({
    queryKey: ["board-versions", auth.user?.user_id, roomId],
    queryFn: () => api.boardHistory(roomId, 100),
    enabled: open && enabled,
  })
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger
        render={<Button variant="outline" size="sm" disabled={!enabled} />}
      >
        <HistoryIcon data-icon="inline-start" /> History
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Board version history</DialogTitle>
          <DialogDescription>
            Restore a saved snapshot as a new revision. Everyone in this room
            receives the restored board.
          </DialogDescription>
        </DialogHeader>
        {query.isPending ? (
          <p role="status">Loading snapshots…</p>
        ) : query.isError ? (
          <div>
            <p role="alert">Could not load version history.</p>
            <Button variant="outline" onClick={() => void query.refetch()}>
              Retry history
            </Button>
          </div>
        ) : query.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No snapshots yet. Board changes are saved automatically.
          </p>
        ) : (
          <ol className="max-h-80 space-y-2 overflow-y-auto">
            {query.data.map((version) => (
              <li
                key={version.version}
                className="flex items-center justify-between gap-3 rounded-lg border p-3"
              >
                <div>
                  <p className="text-sm font-medium">
                    Version {version.version} · {version.element_count} elements
                  </p>
                  {version.saved_at ? (
                    <p className="text-xs text-muted-foreground">
                      {new Date(version.saved_at).toLocaleString()}
                    </p>
                  ) : null}
                </div>
                {auth.user?.role === "teacher" ? (
                  <ConfirmAction
                    title={`Restore version ${version.version}?`}
                    description="This replaces the current canvas for everyone. The current saved version remains in history."
                    confirmLabel="Restore snapshot"
                    onConfirm={async () => {
                      const snapshot = await api.boardRestore(
                        roomId,
                        version.version
                      )
                      onRestored(snapshot.elements)
                      toast.success(`Restored version ${version.version}`)
                      void query.refetch()
                    }}
                  >
                    <Button size="sm" variant="outline">
                      Restore
                    </Button>
                  </ConfirmAction>
                ) : null}
              </li>
            ))}
          </ol>
        )}
      </DialogContent>
    </Dialog>
  )
}
