import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import {
  BookOpenIcon,
  CopyIcon,
  DoorOpenIcon,
  EditIcon,
  LinkIcon,
  MailPlusIcon,
  PlusIcon,
  RadioIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react"

import {
  createClassroom,
  createClassroomOnServer,
  deleteClassroomOnServer,
  inviteClassroomMemberOnServer,
  joinClassroomOnServer,
  leaveClassroomOnServer,
  removeClassroomInvitationOnServer,
  removeClassroomMemberOnServer,
  selectClassroom,
  syncClassroomRosterFromApi,
  syncClassroomsFromApi,
  updateClassroomOnServer,
  useClassrooms,
  useSelectedClassroom,
} from "@/lib/classrooms"
import type { Classroom } from "@/lib/classrooms"
import { useAuth } from "@/lib/auth"
import { ConfirmAction } from "@/components/confirm-action"
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
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type DialogMode = "create" | "join" | "invite" | "edit" | null
type RosterFilter = "all" | "active" | "pending"
type RosterSort = "name" | "joined" | "status"

export function filterRoster(
  members: Classroom["members"],
  search: string,
  status: RosterFilter,
  sort: RosterSort
): Classroom["members"] {
  const needle = search.trim().toLocaleLowerCase()
  return members
    .filter((member) => {
      const matchesSearch =
        !needle ||
        member.name.toLocaleLowerCase().includes(needle) ||
        member.email.toLocaleLowerCase().includes(needle)
      const matchesStatus =
        status === "all" ||
        (status === "active"
          ? member.status === "active"
          : member.status !== "active")
      return matchesSearch && matchesStatus
    })
    .sort((left, right) => {
      if (sort === "joined")
        return Date.parse(right.joined_at) - Date.parse(left.joined_at)
      if (sort === "status")
        return (
          left.status.localeCompare(right.status) ||
          left.name.localeCompare(right.name)
        )
      return left.name.localeCompare(right.name)
    })
}

export function classInviteUrl(code: string, origin: string): string {
  return `${origin}/?join_class=${encodeURIComponent(code)}`
}

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean)
  return words.length === 0
    ? "?"
    : words
        .slice(0, 2)
        .map((word) => word[0])
        .join("")
        .toUpperCase()
}

export function ClassroomDashboard({
  onOpenBoard,
  initialJoinCode,
}: {
  onOpenBoard?: (classroom: Classroom) => void
  initialJoinCode?: string
}) {
  const auth = useAuth()
  const classrooms = useClassrooms()
  const selected = useSelectedClassroom()
  const isTeacher = auth.user?.role === "teacher"
  const [dialog, setDialog] = useState<DialogMode>(null)
  const [className, setClassName] = useState("")
  const [subject, setSubject] = useState("")
  const [joinCode, setJoinCode] = useState("")
  const [memberName, setMemberName] = useState("")
  const [memberEmail, setMemberEmail] = useState("")
  const [editName, setEditName] = useState("")
  const [editSubject, setEditSubject] = useState("")
  const [rosterSearch, setRosterSearch] = useState("")
  const [rosterFilter, setRosterFilter] = useState<RosterFilter>("all")
  const [rosterSort, setRosterSort] = useState<RosterSort>("name")
  const [classSync, setClassSync] = useState<"loading" | "ready" | "error">(
    "loading"
  )
  const [rosterSync, setRosterSync] = useState<"loading" | "ready" | "error">(
    "ready"
  )
  const [writePending, setWritePending] = useState(false)
  const [pendingCreateId, setPendingCreateId] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    if (!auth.user) {
      setClassSync("ready")
      return () => {
        active = false
      }
    }
    setClassSync("loading")
    void syncClassroomsFromApi().then((success) => {
      if (active) setClassSync(success ? "ready" : "error")
    })
    return () => {
      active = false
    }
  }, [auth.user?.user_id])

  useEffect(() => {
    if (initialJoinCode) {
      setJoinCode(initialJoinCode.toUpperCase())
      setDialog("join")
    }
  }, [initialJoinCode])

  useEffect(() => {
    if (selected) {
      setEditName(selected.name)
      setEditSubject(selected.subject)
    }
  }, [selected?.id, selected?.name, selected?.subject])

  useEffect(() => {
    if (!selected || !isTeacher || selected.sync_state !== "synced") {
      setRosterSync("ready")
      return
    }
    let active = true
    setRosterSync("loading")
    void syncClassroomRosterFromApi(selected.id).then((result) => {
      if (active) setRosterSync(result ? "ready" : "error")
    })
    return () => {
      active = false
    }
  }, [isTeacher, selected?.id, selected?.sync_state])

  const visibleRoster = useMemo(
    () =>
      filterRoster(
        selected?.members ?? [],
        rosterSearch,
        rosterFilter,
        rosterSort
      ),
    [rosterFilter, rosterSearch, rosterSort, selected?.members]
  )

  function resetDialog(): void {
    setDialog(null)
    setClassName("")
    setSubject("")
    setMemberName("")
    setMemberEmail("")
    setPendingCreateId(null)
  }

  async function createClass(): Promise<void> {
    if (!isTeacher || !auth.user) return
    if (!className.trim()) {
      toast.error("Give your class a name first")
      return
    }
    let draft = pendingCreateId
      ? classrooms.find((classroom) => classroom.id === pendingCreateId)
      : undefined
    if (!draft) {
      draft = createClassroom({
        name: className.trim(),
        subject,
        owner: {
          id: auth.user.user_id,
          name: auth.user.name,
          email: auth.user.email,
        },
      })
      setPendingCreateId(draft.id)
    }
    setWritePending(true)
    try {
      const created = await createClassroomOnServer(draft)
      if (!created) {
        toast.error(
          "Could not confirm class creation. Retry uses the same request safely."
        )
        return
      }
      await syncClassroomRosterFromApi(created.id)
      toast.success(`${created.name} created`)
      resetDialog()
    } finally {
      setWritePending(false)
    }
  }

  async function retryCreate(classroom: Classroom): Promise<void> {
    setWritePending(true)
    try {
      const synced = await createClassroomOnServer(classroom)
      if (!synced) {
        toast.error(
          "Class creation could not be confirmed. Retry when the server responds."
        )
        return
      }
      await syncClassroomRosterFromApi(synced.id)
      toast.success(`${synced.name} is ready`)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not sync the class"
      )
    } finally {
      setWritePending(false)
    }
  }

  async function joinClass(): Promise<void> {
    if (!auth.user) {
      const target = `/?join_class=${encodeURIComponent(joinCode.trim().toUpperCase())}`
      window.location.assign(`/login?redirect=${encodeURIComponent(target)}`)
      return
    }
    if (!joinCode.trim()) {
      toast.error("Enter the class code")
      return
    }
    setWritePending(true)
    try {
      const joined = await joinClassroomOnServer(joinCode.trim(), {
        id: auth.user.user_id,
        name: auth.user.name,
        email: auth.user.email,
      })
      if (!joined) {
        toast.error(
          "Could not join the class. Check the code and the email on your signed-in account."
        )
        return
      }
      window.history.replaceState({}, "", window.location.pathname)
      resetDialog()
      toast.success(`Joined ${joined.name}`)
    } finally {
      setWritePending(false)
    }
  }

  async function inviteMember(): Promise<void> {
    if (!selected || !isTeacher) return
    if (
      !memberName.trim() ||
      !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(memberEmail.trim())
    ) {
      toast.error("Enter the learner's name and a valid email")
      return
    }
    setWritePending(true)
    try {
      const invitation = await inviteClassroomMemberOnServer(selected.id, {
        name: memberName,
        email: memberEmail,
      })
      resetDialog()
      if (invitation.delivery === "sent") toast.success("Invitation email sent")
      else if (invitation.delivery === "not_configured")
        toast.warning(
          "Invitation saved. Email delivery is not configured; copy the invitation link from the roster."
        )
      else if (invitation.delivery === "failed")
        toast.warning(
          "Invitation saved, but the email could not be sent. Copy the invitation link from the roster."
        )
      else
        toast.success(
          "Invitation created. Copy its link from the roster to share it."
        )
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not create the invitation. Retry to check the server first."
      )
    } finally {
      setWritePending(false)
    }
  }

  async function saveClassDetails(): Promise<void> {
    if (!selected || !isTeacher) return
    if (!editName.trim()) {
      toast.error("Give the class a name")
      return
    }
    setWritePending(true)
    try {
      await updateClassroomOnServer(selected.id, {
        name: editName.trim(),
        description: editSubject.trim(),
      })
      setDialog(null)
      toast.success("Class details updated")
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not update the class"
      )
    } finally {
      setWritePending(false)
    }
  }

  async function copyText(value: string, label: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(`${label} copied`)
    } catch {
      toast.error(`Could not copy the ${label.toLocaleLowerCase()}`)
    }
  }

  function retryRoster(): void {
    if (!selected) return
    setRosterSync("loading")
    void syncClassroomRosterFromApi(selected.id).then((result) =>
      setRosterSync(result ? "ready" : "error")
    )
  }

  function retryClassList(): void {
    setClassSync("loading")
    void syncClassroomsFromApi().then((success) =>
      setClassSync(success ? "ready" : "error")
    )
  }

  const joinUrl =
    selected?.join_code && selected.sync_state === "synced"
      ? classInviteUrl(selected.join_code, window.location.origin)
      : null

  return (
    <>
      <div className="flex flex-col gap-5">
        <div className="flex flex-col gap-3 rounded-xl border bg-card p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <p className="text-sm font-medium">Your classroom workspace</p>
            <p className="truncate text-xs text-muted-foreground">
              Server-backed rosters, invitations, and live sessions.
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <ClassroomSelector
              canCreate={isTeacher}
              onCreate={() => setDialog("create")}
            />
            {!isTeacher ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => setDialog("join")}
              >
                <DoorOpenIcon data-icon="inline-start" />
                Join with code
              </Button>
            ) : null}
            {isTeacher ? (
              <Button size="sm" onClick={() => setDialog("create")}>
                <PlusIcon data-icon="inline-start" />
                New class
              </Button>
            ) : null}
            {classSync === "error" ? (
              <Button size="sm" variant="outline" onClick={retryClassList}>
                Retry class sync
              </Button>
            ) : null}
          </div>
        </div>
        {classSync === "loading" ? (
          <p role="status" className="text-xs text-muted-foreground">
            Loading classes…
          </p>
        ) : null}
        {classSync === "error" ? (
          <p role="alert" className="text-sm text-destructive">
            The class list could not be refreshed. Your cached classes remain
            available.
          </p>
        ) : null}

        {!selected ? (
          <Empty className="min-h-72 rounded-xl border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BookOpenIcon />
              </EmptyMedia>
              <EmptyTitle>
                {isTeacher ? "Create your first class" : "Join a class"}
              </EmptyTitle>
              <EmptyDescription>
                {isTeacher
                  ? "Create a server-backed roster and share its join code."
                  : "Enter a code from your teacher to join their class."}
              </EmptyDescription>
            </EmptyHeader>
            {isTeacher ? (
              <Button onClick={() => setDialog("create")}>
                <PlusIcon data-icon="inline-start" />
                Create a class
              </Button>
            ) : (
              <Button variant="outline" onClick={() => setDialog("join")}>
                <DoorOpenIcon data-icon="inline-start" />
                Join with a code
              </Button>
            )}
          </Empty>
        ) : (
          <>
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              <MetricCard
                icon={<UsersIcon />}
                label="Roster"
                value={String(selected.members.length)}
                detail={`${selected.members.filter((member) => member.status === "active").length} active`}
              />
              <MetricCard
                icon={<MailPlusIcon />}
                label="Invited"
                value={String(
                  selected.members.filter(
                    (member) => member.status !== "active"
                  ).length
                )}
                detail="Awaiting first join"
              />
              <MetricCard
                icon={<RadioIcon />}
                label="Live session"
                value={selected.active_room_id ? "Ready" : "Not started"}
                detail={
                  selected.active_room_id ?? "Start a board when class begins"
                }
              />
            </div>

            {selected.sync_state === "pending" ||
            selected.sync_state === "error" ? (
              <div
                className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3"
                role="status"
              >
                <p className="text-sm">
                  {selected.sync_state === "pending"
                    ? "Class creation is still syncing."
                    : "Class creation needs a retry."}
                </p>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={writePending}
                  onClick={() => void retryCreate(selected)}
                >
                  {writePending ? "Syncing…" : "Retry class sync"}
                </Button>
              </div>
            ) : null}
            {rosterSync === "loading" ? (
              <p role="status" className="text-xs text-muted-foreground">
                Updating the roster…
              </p>
            ) : null}
            {rosterSync === "error" ? (
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-destructive/30 p-3">
                <p className="text-sm">The roster could not be refreshed.</p>
                <Button size="sm" variant="outline" onClick={retryRoster}>
                  Retry roster sync
                </Button>
              </div>
            ) : null}

            <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(18rem,0.42fr)]">
              <Card>
                <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
                  <div className="flex min-w-0 flex-col gap-1">
                    <CardTitle className="truncate">{selected.name}</CardTitle>
                    <CardDescription>
                      {selected.subject} · created{" "}
                      {new Date(selected.created_at).toLocaleDateString()}
                    </CardDescription>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge variant="secondary">
                      {selected.members.length} people
                    </Badge>
                    {isTeacher ? (
                      <Button
                        size="icon-sm"
                        variant="outline"
                        aria-label="Edit class"
                        onClick={() => setDialog("edit")}
                      >
                        <EditIcon />
                      </Button>
                    ) : null}
                  </div>
                </CardHeader>
                <CardContent>
                  <div className="mb-3 grid gap-2 sm:grid-cols-3">
                    <Input
                      aria-label="Search roster"
                      placeholder="Search roster"
                      value={rosterSearch}
                      onChange={(event) => setRosterSearch(event.target.value)}
                    />
                    <Select
                      value={rosterFilter}
                      onValueChange={(value) => setRosterFilter(value || "all")}
                    >
                      <SelectTrigger aria-label="Filter roster">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="all">All members</SelectItem>
                        <SelectItem value="active">Active</SelectItem>
                        <SelectItem value="pending">
                          Pending invitations
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <Select
                      value={rosterSort}
                      onValueChange={(value) => setRosterSort(value || "name")}
                    >
                      <SelectTrigger aria-label="Sort roster">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="name">Name A–Z</SelectItem>
                        <SelectItem value="joined">Recently joined</SelectItem>
                        <SelectItem value="status">Status</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  {selected.members.length === 0 ? (
                    <Empty className="min-h-48 border border-dashed">
                      <EmptyHeader>
                        <EmptyTitle>No learners yet</EmptyTitle>
                        <EmptyDescription>
                          Invite a learner or share the class code.
                        </EmptyDescription>
                      </EmptyHeader>
                    </Empty>
                  ) : visibleRoster.length === 0 ? (
                    <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
                      No roster entries match these filters.
                    </p>
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
                        {visibleRoster.map((member) => (
                          <TableRow key={member.id}>
                            <TableCell>
                              <div className="flex items-center gap-2">
                                <Avatar size="sm">
                                  <AvatarFallback>
                                    {initials(member.name)}
                                  </AvatarFallback>
                                </Avatar>
                                <div className="min-w-0">
                                  <p className="truncate font-medium">
                                    {member.name}
                                  </p>
                                  <p className="truncate text-xs text-muted-foreground">
                                    {member.email || "No email on file"}
                                  </p>
                                </div>
                              </div>
                            </TableCell>
                            <TableCell>
                              <Badge
                                variant={
                                  member.status === "active"
                                    ? "default"
                                    : "secondary"
                                }
                              >
                                {member.status === "invited"
                                  ? "pending"
                                  : member.status}
                              </Badge>
                            </TableCell>
                            <TableCell className="text-muted-foreground">
                              {member.status === "pending" ||
                              member.status === "invited"
                                ? "Invitation pending"
                                : new Date(
                                    member.joined_at
                                  ).toLocaleDateString()}
                            </TableCell>
                            <TableCell className="text-right">
                              {member.role === "teacher" ? (
                                <Badge variant="outline">Owner</Badge>
                              ) : isTeacher ? (
                                <ConfirmAction
                                  title={
                                    member.status === "active"
                                      ? "Remove learner?"
                                      : "Cancel invitation?"
                                  }
                                  description={
                                    member.status === "active"
                                      ? `${member.name} will be removed from this class roster.`
                                      : `The pending invitation for ${member.name} will be removed.`
                                  }
                                  confirmLabel={
                                    member.status === "active"
                                      ? "Remove learner"
                                      : "Cancel invitation"
                                  }
                                  onConfirm={async () => {
                                    if (
                                      member.status === "pending" ||
                                      member.status === "invited"
                                    )
                                      await removeClassroomInvitationOnServer(
                                        selected.id,
                                        member.id
                                      )
                                    else if (
                                      !(await removeClassroomMemberOnServer(
                                        selected.id,
                                        member.id
                                      ))
                                    )
                                      throw new Error(
                                        "Could not remove this learner. Retry when the server responds."
                                      )
                                  }}
                                >
                                  <Button
                                    size="icon-sm"
                                    variant="ghost"
                                    aria-label={`Remove ${member.name}`}
                                  >
                                    <Trash2Icon />
                                  </Button>
                                </ConfirmAction>
                              ) : null}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </CardContent>
                <CardFooter className="flex-wrap gap-2">
                  {isTeacher ? (
                    <Button size="sm" onClick={() => setDialog("invite")}>
                      <MailPlusIcon data-icon="inline-start" />
                      Invite learner
                    </Button>
                  ) : null}
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => onOpenBoard?.(selected)}
                  >
                    <RadioIcon data-icon="inline-start" />
                    Open live board
                  </Button>
                  {isTeacher ? (
                    <ConfirmAction
                      title="Delete this class?"
                      description="The class, roster, and pending invitations will be removed from the server."
                      confirmLabel="Delete class"
                      onConfirm={() => deleteClassroomOnServer(selected.id)}
                    >
                      <Button size="sm" variant="outline">
                        <Trash2Icon data-icon="inline-start" />
                        Delete class
                      </Button>
                    </ConfirmAction>
                  ) : auth.user ? (
                    <ConfirmAction
                      title="Leave this class?"
                      description="You will be removed from the server roster and can rejoin with a class code."
                      confirmLabel="Leave class"
                      onConfirm={() => leaveClassroomOnServer(selected.id)}
                    >
                      <Button size="sm" variant="outline">
                        <DoorOpenIcon data-icon="inline-start" />
                        Leave class
                      </Button>
                    </ConfirmAction>
                  ) : null}
                </CardFooter>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <MailPlusIcon data-icon="inline-start" />
                    Invite this class
                  </CardTitle>
                  <CardDescription>
                    Share the server-issued class code. Learner invitations show
                    whether email delivery was confirmed.
                  </CardDescription>
                </CardHeader>
                <CardContent className="flex flex-col gap-4">
                  {joinUrl && selected.join_code ? (
                    <>
                      <div className="flex items-center justify-between gap-3 rounded-lg border bg-muted/40 p-3">
                        <span className="font-mono text-2xl font-semibold tracking-[0.2em]">
                          {selected.join_code}
                        </span>
                        <Button
                          size="icon-sm"
                          variant="outline"
                          aria-label="Copy class code"
                          onClick={() =>
                            void copyText(selected.join_code, "Class code")
                          }
                        >
                          <CopyIcon />
                        </Button>
                      </div>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => void copyText(joinUrl, "Join link")}
                      >
                        <LinkIcon data-icon="inline-start" />
                        Copy class join link
                      </Button>
                    </>
                  ) : (
                    <p className="text-sm text-muted-foreground">
                      The server code will appear after class creation is
                      confirmed.
                    </p>
                  )}
                  {selected.invitations
                    ?.filter((invitation) => invitation.status === "pending")
                    .map((invitation) => (
                      <div
                        key={invitation.id}
                        className="rounded-lg border p-3"
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0">
                            <p className="truncate text-sm font-medium">
                              {invitation.name}
                            </p>
                            <p className="truncate text-xs text-muted-foreground">
                              {invitation.email}
                            </p>
                          </div>
                          <Badge variant="secondary">
                            {invitation.delivery === "sent"
                              ? "email sent"
                              : invitation.delivery === "failed"
                                ? "email failed"
                                : invitation.delivery === "not_configured"
                                  ? "email not configured"
                                  : "pending"}
                          </Badge>
                        </div>
                        <Button
                          size="sm"
                          variant="outline"
                          className="mt-3"
                          onClick={() =>
                            void copyText(
                              classInviteUrl(
                                invitation.join_code,
                                window.location.origin
                              ),
                              "Invitation link"
                            )
                          }
                        >
                          <CopyIcon data-icon="inline-start" />
                          Copy invitation link
                        </Button>
                      </div>
                    ))}
                  {!isTeacher ? (
                    <p className="text-xs text-muted-foreground">
                      Only teachers can create learner invitations.
                    </p>
                  ) : null}
                </CardContent>
              </Card>
            </div>
          </>
        )}
      </div>

      <Dialog
        open={dialog === "create"}
        onOpenChange={(open) => !open && resetDialog()}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create a class</DialogTitle>
            <DialogDescription>
              The server issues the join code after the class is created.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="class-name">Class name</FieldLabel>
              <Input
                id="class-name"
                value={className}
                onChange={(event) => setClassName(event.target.value)}
                placeholder="Systems 101"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="class-subject">Subject</FieldLabel>
              <Input
                id="class-subject"
                value={subject}
                onChange={(event) => setSubject(event.target.value)}
                placeholder="Distributed systems"
              />
              <FieldDescription>
                Optional context shown beside the class name.
              </FieldDescription>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={resetDialog}>
              Cancel
            </Button>
            <Button
              disabled={writePending || !className.trim()}
              onClick={() => void createClass()}
            >
              {writePending
                ? "Creating…"
                : pendingCreateId
                  ? "Retry class creation"
                  : "Create class"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={dialog === "edit"}
        onOpenChange={(open) => !open && setDialog(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit class</DialogTitle>
            <DialogDescription>
              Rename the class or change its subject.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="edit-class-name">Class name</FieldLabel>
              <Input
                id="edit-class-name"
                value={editName}
                onChange={(event) => setEditName(event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-class-subject">Subject</FieldLabel>
              <Input
                id="edit-class-subject"
                value={editSubject}
                onChange={(event) => setEditSubject(event.target.value)}
              />
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialog(null)}>
              Cancel
            </Button>
            <Button
              disabled={writePending}
              onClick={() => void saveClassDetails()}
            >
              {writePending ? "Saving…" : "Save changes"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={dialog === "join"}
        onOpenChange={(open) => !open && resetDialog()}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Join a class</DialogTitle>
            <DialogDescription>
              Enter a class code or an invitation link you received.
            </DialogDescription>
          </DialogHeader>
          {auth.user ? (
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="join-class-code">
                  Class or invitation code
                </FieldLabel>
                <Input
                  id="join-class-code"
                  value={joinCode}
                  onChange={(event) =>
                    setJoinCode(event.target.value.toUpperCase())
                  }
                  placeholder="ABC123"
                  autoCapitalize="characters"
                  spellCheck={false}
                />
              </Field>
              <p className="text-xs text-muted-foreground">
                Signed in as {auth.user.email}. Invitation links must match this
                email address.
              </p>
            </FieldGroup>
          ) : (
            <div className="flex flex-col gap-3">
              <p className="text-sm text-muted-foreground">
                Sign in with the invited email address before joining.
              </p>
              <a
                className="text-sm font-medium text-primary underline"
                href={`/login?redirect=${encodeURIComponent(`/?join_class=${encodeURIComponent(joinCode)}`)}`}
              >
                Sign in to join this class
              </a>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={resetDialog}>
              Cancel
            </Button>
            {auth.user ? (
              <Button
                disabled={writePending || !joinCode.trim()}
                onClick={() => void joinClass()}
              >
                {writePending ? "Joining…" : "Join class"}
              </Button>
            ) : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={dialog === "invite"}
        onOpenChange={(open) => !open && resetDialog()}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Invite a learner</DialogTitle>
            <DialogDescription>
              The invitation is stored on the server. We’ll report whether email
              delivery was confirmed.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="member-name">Learner name</FieldLabel>
              <Input
                id="member-name"
                value={memberName}
                onChange={(event) => setMemberName(event.target.value)}
                placeholder="Lin Chen"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="member-email">Learner email</FieldLabel>
              <Input
                id="member-email"
                type="email"
                value={memberEmail}
                onChange={(event) => setMemberEmail(event.target.value)}
                placeholder="lin@example.com"
              />
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={resetDialog}>
              Cancel
            </Button>
            <Button disabled={writePending} onClick={() => void inviteMember()}>
              {writePending ? "Creating…" : "Create invitation"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function ClassroomSelector({
  onCreate,
  canCreate,
}: {
  onCreate?: () => void
  canCreate: boolean
}) {
  const classrooms = useClassrooms()
  const selected = useSelectedClassroom()
  if (classrooms.length === 0)
    return canCreate ? (
      <Button size="sm" variant="outline" onClick={onCreate}>
        <PlusIcon data-icon="inline-start" />
        Set up a class
      </Button>
    ) : null
  return (
    <Select
      value={selected?.id ?? null}
      onValueChange={(value) =>
        selectClassroom(typeof value === "string" ? value : null)
      }
    >
      <SelectTrigger aria-label="Select a class" className="w-full sm:w-56">
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
