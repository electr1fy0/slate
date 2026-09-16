import { useEffect, useState } from "react"
import { toast } from "sonner"
import {
  BookOpenIcon,
  CheckIcon,
  CopyIcon,
  DoorOpenIcon,
  GraduationCapIcon,
  MailPlusIcon,
  PlusIcon,
  RadioIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react"

import {
  addClassroomMember,
  createClassroomOnServer,
  createClassroom,
  joinClassroom,
  joinClassroomOnServer,
  removeClassroomMember,
  removeClassroomMemberOnServer,
  selectClassroom,
  syncClassroomRosterFromApi,
  syncClassroomsFromApi,
  useClassrooms,
  useSelectedClassroom,
} from "@/lib/classrooms"
import type { Classroom } from "@/lib/classrooms"
import { useAuth } from "@/lib/auth"
import { newRoomCode } from "@/lib/room-code"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) return "?"
  return words.slice(0, 2).map((word) => word[0]).join("").toUpperCase()
}

export function classroomSelectorValue(selected: Classroom | null): string | null {
  return selected?.id ?? null
}

export function selectClassroomFromValue(value: unknown): void {
  selectClassroom(typeof value === "string" ? value : null)
}

export function withSelectedClassroom<T>(
  selected: Classroom | null,
  action: (classroom: Classroom) => T,
): T | undefined {
  return selected ? action(selected) : undefined
}

export function handleClassroomJoinResult(
  joined: Classroom | null,
  onFailure: () => void,
  onSuccess: (classroom: Classroom) => void,
): void {
  if (!joined) {
    onFailure()
    return
  }
  onSuccess(joined)
}

export function ClassroomSelector({
  onCreate,
  className,
}: {
  onCreate?: () => void
  className?: string
}) {
  const classrooms = useClassrooms()
  const selected = useSelectedClassroom()

  if (classrooms.length === 0) {
    return (
      <Button variant="outline" size="sm" onClick={onCreate} className={className}>
        <PlusIcon data-icon="inline-start" />
        Set up a class
      </Button>
    )
  }

  return (
    <Select
      value={classroomSelectorValue(selected)}
      onValueChange={selectClassroomFromValue}
    >
      <SelectTrigger className={className ?? "w-full sm:w-56"} aria-label="Select a class">
        <SelectValue placeholder="Select a class" />
      </SelectTrigger>
      <SelectContent>
        {classrooms.map((classroom) => (
          <SelectItem key={classroom.id} value={classroom.id}>
            {classroom.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

type DialogMode = "create" | "join" | "invite" | null

export function ClassroomDashboard({
  onOpenBoard,
}: {
  onOpenBoard?: (classroom: Classroom) => void
}) {
  const auth = useAuth()
  const selected = useSelectedClassroom()
  const [dialog, setDialog] = useState<DialogMode>(null)
  const [className, setClassName] = useState("")
  const [subject, setSubject] = useState("")
  const [joinCode, setJoinCode] = useState("")
  const [joinName, setJoinName] = useState("")
  const [joinEmail, setJoinEmail] = useState("")
  const [memberName, setMemberName] = useState("")
  const [memberEmail, setMemberEmail] = useState("")

  useEffect(() => {
    void syncClassroomsFromApi()
  }, [])

  useEffect(() => {
    if (selected && auth.user?.role === "teacher") void syncClassroomRosterFromApi(selected.id)
  }, [auth.user?.role, selected?.id])

  function resetDialog(): void {
    setDialog(null)
    setClassName("")
    setSubject("")
    setJoinCode("")
    setJoinName("")
    setJoinEmail("")
    setMemberName("")
    setMemberEmail("")
  }

  function createClass(): void {
    const name = className.trim()
    if (!name) {
      toast.error("Give your class a name first")
      return
    }
    const created = createClassroom({
      name,
      subject,
      owner: {
        id: auth.user?.user_id ?? "local-teacher",
        name: auth.user?.name || "Class teacher",
        email: auth.user?.email,
      },
    })
    resetDialog()
    toast.success(`${created.name} is ready`, { description: `Invite code ${created.join_code}` })
    void createClassroomOnServer(created).then((synced) => {
      if (synced) toast.success("Class synced to your workspace")
    })
  }

  async function joinClass(): Promise<void> {
    const name = joinName.trim() || auth.user?.name.trim() || "New learner"
    const member = {
      id: auth.user?.user_id,
      name,
      email: joinEmail.trim() || auth.user?.email,
    }
    const joined = joinClassroom(joinCode, member) ?? await joinClassroomOnServer(joinCode, member)
    handleClassroomJoinResult(
      joined,
      () => toast.error("That invite code does not match a class in this workspace"),
      (classroom) => {
        resetDialog()
        toast.success(`Joined ${classroom.name}`)
      },
    )
  }

  function inviteMember(): void {
    withSelectedClassroom(selected, (classroom) => {
      if (!memberName.trim()) {
        toast.error("Add the learner's name")
        return
      }
      addClassroomMember(classroom.id, {
        name: memberName,
        email: memberEmail,
        status: "invited",
      })
      resetDialog()
      toast.success("Learner added to the roster")
    })
  }

  async function copyCode(): Promise<void> {
    await withSelectedClassroom(selected, async (classroom) => {
      try {
        await navigator.clipboard.writeText(classroom.join_code)
        toast.success("Invite code copied")
      } catch {
        toast.error("Could not copy the invite code")
      }
    })
  }

  function removeMember(memberId: string, name: string): void {
    withSelectedClassroom(selected, (classroom) => {
      removeClassroomMember(classroom.id, memberId)
      toast.info(`${name} removed from the roster`)
      void removeClassroomMemberOnServer(classroom.id, memberId).then((removed) => {
        if (!removed) toast.warning("Removed locally; the server roster could not be updated")
      })
    })
  }

  return (
    <>
      <div className="flex flex-col gap-5">
        <div className="flex flex-col gap-3 rounded-xl border bg-card p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
              <GraduationCapIcon />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium">Your classroom workspace</p>
              <p className="truncate text-xs text-muted-foreground">
                Choose a class to manage its roster and live session.
              </p>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <ClassroomSelector onCreate={() => setDialog("create")} />
            <Button variant="outline" size="sm" onClick={() => setDialog("join")}>
              <DoorOpenIcon data-icon="inline-start" />
              Join with code
            </Button>
            <Button size="sm" onClick={() => setDialog("create")}>
              <PlusIcon data-icon="inline-start" />
              New class
            </Button>
          </div>
        </div>

        {!selected ? (
          <Empty className="min-h-72 rounded-xl border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BookOpenIcon />
              </EmptyMedia>
              <EmptyTitle>Create your first class</EmptyTitle>
              <EmptyDescription>
                Keep a roster, share a short join code, and bring the class into a live board.
              </EmptyDescription>
            </EmptyHeader>
            <Button onClick={() => setDialog("create")}>
              <PlusIcon data-icon="inline-start" />
              Create a class
            </Button>
          </Empty>
        ) : (
          <>
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <MetricCard
                icon={<UsersIcon />}
                label="Roster"
                value={String(selected.members.length)}
                detail={`${selected.members.filter((member) => member.status === "active").length} active now`}
              />
              <MetricCard
                icon={<MailPlusIcon />}
                label="Invited"
                value={String(selected.members.filter((member) => member.status === "invited").length)}
                detail="Awaiting first join"
              />
              <MetricCard
                icon={<RadioIcon />}
                label="Live session"
                value={selected.active_room_id ? "Ready" : "Not started"}
                detail={selected.active_room_id ?? "Start a board when class begins"}
              />
              <MetricCard
                icon={<CheckIcon />}
                label="Workspace"
                value="Synced"
                detail="Classroom API connected"
              />
            </div>

            <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(18rem,0.42fr)]">
              <Card>
                <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
                  <div className="flex min-w-0 flex-col gap-1">
                    <CardTitle className="truncate">{selected.name}</CardTitle>
                    <CardDescription>
                      {selected.subject} · created {new Date(selected.created_at).toLocaleDateString()}
                    </CardDescription>
                  </div>
                  <Badge variant="secondary">{selected.members.length} people</Badge>
                </CardHeader>
                <CardContent>
                  {selected.members.length === 0 ? (
                    <Empty className="min-h-48 border border-dashed">
                      <EmptyHeader>
                        <EmptyTitle>No learners yet</EmptyTitle>
                        <EmptyDescription>Invite a learner or share the class code.</EmptyDescription>
                      </EmptyHeader>
                    </Empty>
                  ) : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Learner</TableHead>
                          <TableHead>Status</TableHead>
                          <TableHead>Joined</TableHead>
                          <TableHead className="w-10 text-right">
                            <span className="sr-only">Actions</span>
                          </TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {selected.members.map((member) => (
                          <TableRow key={member.id}>
                            <TableCell>
                              <div className="flex items-center gap-2">
                                <Avatar size="sm">
                                  <AvatarFallback>{initials(member.name)}</AvatarFallback>
                                </Avatar>
                                <div className="min-w-0">
                                  <p className="truncate font-medium">{member.name}</p>
                                  <p className="truncate text-xs text-muted-foreground">
                                    {member.email || "No email on file"}
                                  </p>
                                </div>
                              </div>
                            </TableCell>
                            <TableCell>
                              <Badge variant={member.status === "active" ? "default" : "secondary"}>
                                {member.status}
                              </Badge>
                            </TableCell>
                            <TableCell className="text-muted-foreground">
                              {member.status === "invited" ? "Invite sent" : new Date(member.joined_at).toLocaleDateString()}
                            </TableCell>
                            <TableCell className="text-right">
                              {member.role === "teacher" ? (
                                <Badge variant="outline">Owner</Badge>
                              ) : (
                                <Button
                                  size="icon-sm"
                                  variant="ghost"
                                  aria-label={`Remove ${member.name}`}
                                  onClick={() => removeMember(member.id, member.name)}
                                >
                                  <Trash2Icon />
                                </Button>
                              )}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </CardContent>
                <CardFooter className="flex-wrap gap-2">
                  <Button size="sm" onClick={() => setDialog("invite")}>
                    <MailPlusIcon data-icon="inline-start" />
                    Add learner
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => onOpenBoard?.(selected)}
                  >
                    <RadioIcon data-icon="inline-start" />
                    Open live board
                  </Button>
                </CardFooter>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <MailPlusIcon data-icon="inline-start" />
                    Invite this class
                  </CardTitle>
                  <CardDescription>
                    Share the code in your classroom. Learners join the synced roster when they enter it.
                  </CardDescription>
                </CardHeader>
                <CardContent className="flex flex-col gap-4">
                  <div className="flex items-center justify-between gap-3 rounded-lg border bg-muted/40 p-3">
                    <span className="font-mono text-2xl font-semibold tracking-[0.2em]">
                      {selected.join_code}
                    </span>
                    <Button size="icon-sm" variant="outline" aria-label="Copy invite code" onClick={() => void copyCode()}>
                      <CopyIcon />
                    </Button>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    Add placeholders locally, or have learners join to create server-backed roster entries.
                  </p>
                </CardContent>
              </Card>
            </div>
          </>
        )}
      </div>

      <Dialog open={dialog === "create"} onOpenChange={(open) => !open && resetDialog()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create a class</DialogTitle>
            <DialogDescription>Set up a reusable home for your roster and live sessions.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="class-name">Class name</FieldLabel>
              <Input id="class-name" value={className} onChange={(event) => setClassName(event.target.value)} placeholder="Systems 101" />
            </Field>
            <Field>
              <FieldLabel htmlFor="class-subject">Subject</FieldLabel>
              <Input id="class-subject" value={subject} onChange={(event) => setSubject(event.target.value)} placeholder="Distributed systems" />
              <FieldDescription>Optional context shown beside the class name.</FieldDescription>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={resetDialog}>Cancel</Button>
            <Button onClick={createClass}>Create class</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={dialog === "join"} onOpenChange={(open) => !open && resetDialog()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Join a class</DialogTitle>
            <DialogDescription>Enter the six-character code shared by your teacher.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="join-class-code">Invite code</FieldLabel>
              <Input
                id="join-class-code"
                value={joinCode}
                onChange={(event) => setJoinCode(event.target.value.toUpperCase())}
                placeholder="ABC123"
                autoCapitalize="characters"
                spellCheck={false}
              />
            </Field>
            {!auth.user ? (
              <Field>
                <FieldLabel htmlFor="join-name">Your name</FieldLabel>
                <Input id="join-name" value={joinName} onChange={(event) => setJoinName(event.target.value)} placeholder="Sam Lee" />
              </Field>
            ) : null}
            {!auth.user ? (
              <Field>
                <FieldLabel htmlFor="join-email">Email (optional)</FieldLabel>
                <Input id="join-email" type="email" value={joinEmail} onChange={(event) => setJoinEmail(event.target.value)} placeholder="sam@example.com" />
              </Field>
            ) : null}
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={resetDialog}>Cancel</Button>
            <Button onClick={joinClass}>Join class</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={dialog === "invite"} onOpenChange={(open) => !open && resetDialog()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add a learner</DialogTitle>
            <DialogDescription>Add a roster placeholder now, then share the class code for self-join.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="member-name">Learner name</FieldLabel>
              <Input id="member-name" value={memberName} onChange={(event) => setMemberName(event.target.value)} placeholder="Lin Chen" />
            </Field>
            <Field>
              <FieldLabel htmlFor="member-email">Email (optional)</FieldLabel>
              <Input id="member-email" type="email" value={memberEmail} onChange={(event) => setMemberEmail(event.target.value)} placeholder="lin@example.com" />
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={resetDialog}>Cancel</Button>
            <Button onClick={inviteMember}>Add to roster</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function MetricCard({
  icon,
  label,
  value,
  detail,
}: {
  icon: React.ReactNode
  label: string
  value: string
  detail: string
}) {
  return (
    <Card size="sm">
      <CardHeader className="flex-row items-start justify-between space-y-0">
        <CardDescription>{label}</CardDescription>
        <span className="text-muted-foreground [&_svg]:size-4">{icon}</span>
      </CardHeader>
      <CardContent>
        <p className="text-xl font-semibold tracking-tight">{value}</p>
        <p className="truncate text-xs text-muted-foreground">{detail}</p>
      </CardContent>
    </Card>
  )
}

export function classBoardRoom(classroom: Classroom): string {
  return classroom.active_room_id ?? `class-${classroom.id.replace(/[^a-z0-9]/gi, "").slice(-8)}-${newRoomCode().slice(0, 4)}`
}
