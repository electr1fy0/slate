import { useState } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import {
  ActivityIcon,
  ClipboardListIcon,
  HouseIcon,
  MenuIcon,
  PlusIcon,
  PresentationIcon,
} from "lucide-react"

import { useAuth } from "@/lib/auth"
import { newRoomCode } from "@/lib/room-code"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"

const baseActions = [
  { label: "Dashboard", to: "/", icon: HouseIcon },
  { label: "New board", to: "/board", icon: PresentationIcon },
]

export function QuickActions() {
  const auth = useAuth()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const actions = auth.user?.role === "teacher"
    ? [
        ...baseActions,
        { label: "Teacher studio", to: "/teacher", icon: ClipboardListIcon },
        { label: "Continuum", to: "/ops", icon: ActivityIcon },
      ]
    : baseActions

  function newBoard(): void {
    setOpen(false)
    void navigate({ to: "/board/$roomId", params: { roomId: newRoomCode() } })
  }

  return (
    <>
      <div className="hidden items-center gap-1 sm:flex">
        <Button
          variant="outline"
          size="sm"
          nativeButton={false}
          render={<Link to="/" hash="assessments" />}
        >
          <ClipboardListIcon data-icon="inline-start" />
          Timed assessments
        </Button>
      </div>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger
          render={
            <Button variant="outline" size="icon-sm" className="sm:hidden" aria-label="Open quick actions" />
          }
        >
          <MenuIcon />
        </SheetTrigger>
        <SheetContent side="right">
          <SheetHeader>
            <SheetTitle>Quick actions</SheetTitle>
            <SheetDescription>Move between the classroom surfaces from any screen.</SheetDescription>
          </SheetHeader>
          <div className="flex flex-col gap-2 px-4">
            <Button className="justify-start" onClick={newBoard}>
              <PlusIcon data-icon="inline-start" />
              New board
            </Button>
            {actions.map((action) => (
              <Button
                key={action.to}
                variant="outline"
                className="justify-start"
                nativeButton={false}
                render={<Link to={action.to} />}
                onClick={() => setOpen(false)}
              >
                <action.icon data-icon="inline-start" />
                {action.label}
              </Button>
            ))}
          </div>
        </SheetContent>
      </Sheet>
    </>
  )
}
