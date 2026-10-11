import { useSyncExternalStore } from "react"

import { api } from "./api"
import type {
  ClassroomInvitation,
  ClassroomMemberRecord,
  ClassroomRecord,
} from "./api"
import { getAuth } from "./auth"
import type { Role } from "./types"

export type ClassroomMemberStatus = "active" | "invited" | "pending"

export interface ClassroomMember {
  id: string
  name: string
  email: string
  role: Role
  status: ClassroomMemberStatus
  joined_at: string
}

export interface Classroom {
  id: string
  name: string
  subject: string
  join_code: string
  created_at: string
  members: ClassroomMember[]
  invitations?: ClassroomInvitation[]
  sync_state?: "pending" | "synced" | "error"
  active_room_id?: string
}

interface MemberInput {
  id?: string
  name: string
  email?: string
  role?: Role
  status?: ClassroomMemberStatus
}

const STORAGE_KEY = "slate.classrooms"
const SELECTED_KEY = "slate.selected-classroom"
const MAX_CLASSROOMS = 20
const SERVER_SNAPSHOT: Classroom[] = []

const classroomCache = new Map<string, Classroom[]>()
const selectedClassroomCache = new Map<string, string | null>()
const listeners = new Set<() => void>()

function accountScope(): string {
  return getAuth().user?.user_id ?? "anonymous"
}

function scopedKey(base: string, account = accountScope()): string {
  return account === "anonymous"
    ? base
    : `${base}.${encodeURIComponent(account)}`
}

function nowIso(): string {
  return new Date().toISOString()
}

function randomId(prefix: string): string {
  const suffix =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID().slice(0, 8)
      : Math.random().toString(36).slice(2, 10)
  return `${prefix}-${suffix}`
}

function readClassrooms(account = accountScope()): Classroom[] {
  if (typeof window === "undefined") return []
  try {
    const raw = window.localStorage.getItem(scopedKey(STORAGE_KEY, account))
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []

    return parsed.filter(isClassroom).slice(0, MAX_CLASSROOMS)
  } catch {
    return []
  }
}

function isClassroom(value: unknown): value is Classroom {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<Classroom>
  return (
    typeof candidate.id === "string" &&
    typeof candidate.name === "string" &&
    typeof candidate.subject === "string" &&
    typeof candidate.join_code === "string" &&
    typeof candidate.created_at === "string" &&
    Array.isArray(candidate.members) &&
    candidate.members.every(isMember)
  )
}

function isMember(value: unknown): value is ClassroomMember {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<ClassroomMember>
  return (
    typeof candidate.id === "string" &&
    typeof candidate.name === "string" &&
    typeof candidate.email === "string" &&
    (candidate.role === "teacher" || candidate.role === "student") &&
    (candidate.status === "active" ||
      candidate.status === "invited" ||
      candidate.status === "pending") &&
    typeof candidate.joined_at === "string"
  )
}

function readSelectedClassroom(account = accountScope()): string | null {
  if (typeof window === "undefined") return null
  try {
    return window.localStorage.getItem(scopedKey(SELECTED_KEY, account))
  } catch {
    return null
  }
}

function classroomSnapshot(account: string): Classroom[] {
  if (!classroomCache.has(account))
    classroomCache.set(account, readClassrooms(account))
  return classroomCache.get(account) ?? []
}

function getClassroomSnapshot(): Classroom[] {
  return classroomSnapshot(accountScope())
}

function selectedSnapshot(account: string): string | null {
  if (!selectedClassroomCache.has(account))
    selectedClassroomCache.set(account, readSelectedClassroom(account))
  return selectedClassroomCache.get(account) ?? null
}

function getSelectedSnapshot(): string | null {
  return selectedSnapshot(accountScope())
}

function getServerClassrooms(): Classroom[] {
  return SERVER_SNAPSHOT
}

function getServerSelected(): string | null {
  return null
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function notify(): void {
  for (const listener of listeners) listener()
}

function persistClassrooms(next: Classroom[], account = accountScope()): void {
  const snapshot = next.slice(0, MAX_CLASSROOMS)
  classroomCache.set(account, snapshot)
  try {
    window.localStorage.setItem(
      scopedKey(STORAGE_KEY, account),
      JSON.stringify(snapshot)
    )
  } catch {
    // Keep the in-memory workspace usable when storage is blocked or full.
  }
  notify()
}

function persistSelected(id: string | null, account = accountScope()): void {
  selectedClassroomCache.set(account, id)
  try {
    if (id) window.localStorage.setItem(scopedKey(SELECTED_KEY, account), id)
    else window.localStorage.removeItem(scopedKey(SELECTED_KEY, account))
  } catch {
    // Selection is still available for the current session.
  }
  notify()
}

function fromServer(
  record: ClassroomRecord,
  previous?: Classroom,
  members: ClassroomMember[] = previous?.members ?? []
): Classroom {
  return {
    id: record.id,
    name: record.name,
    subject:
      record.description === undefined
        ? (previous?.subject ?? "General studies")
        : record.description.trim(),
    join_code: record.join_code ?? "",
    created_at: record.created_at || previous?.created_at || nowIso(),
    members,
    invitations: previous?.invitations ?? [],
    sync_state: "synced",
    active_room_id: previous?.active_room_id,
  }
}

function membersFromServer(
  rows: ClassroomMemberRecord[],
  previous: Classroom | undefined,
  invitations: ClassroomInvitation[] = previous?.invitations ?? []
): ClassroomMember[] {
  const owner = previous?.members.find((member) => member.role === "teacher")
  const members: ClassroomMember[] = rows.map((member) => ({
    id: member.user_id,
    name: member.name,
    email: member.email,
    role: member.role,
    status: "active" as const,
    joined_at: member.joined_at,
  }))
  if (owner && !members.some((member) => member.id === owner.id))
    members.unshift(owner)
  for (const invitation of invitations.filter(
    (item) => item.status === "pending"
  )) {
    if (
      members.some(
        (member) =>
          member.email.toLowerCase() === invitation.email.toLowerCase()
      )
    )
      continue
    members.push({
      id: invitation.id,
      name: invitation.name,
      email: invitation.email,
      role: "student",
      status: "pending",
      joined_at: invitation.created_at,
    })
  }
  return members
}

export function mergeJoinedMember(
  members: ClassroomMember[],
  nextMember: ClassroomMember,
  email?: string
): ClassroomMember[] {
  return members.some(
    (item) => item.id === nextMember.id || (email && item.email === email)
  )
    ? members.map((item) =>
        item.id === nextMember.id || (email && item.email === email)
          ? { ...item, status: "active" as const }
          : item
      )
    : [...members, nextMember]
}

function replaceClassroom(
  previousId: string,
  next: Classroom,
  account = accountScope()
): Classroom {
  const current = classroomSnapshot(account)
  persistClassrooms(
    [
      next,
      ...current.filter(
        (item) => item.id !== previousId && item.id !== next.id
      ),
    ],
    account
  )
  if (selectedSnapshot(account) === previousId)
    persistSelected(next.id, account)
  return next
}

export async function syncClassroomsFromApi(): Promise<boolean> {
  const account = accountScope()
  try {
    const records = await api.listClassrooms()
    if (accountScope() !== account) return false
    const local = classroomSnapshot(account)
    const remoteIds = new Set(records.map((record) => record.id))
    const adoptedLocalIds = new Set<string>()
    const mapped = records.map((record) => {
      const exact = local.find((item) => item.id === record.id)
      const pendingMatch =
        !exact && record.owner_id === getAuth().user?.user_id
          ? local.find(
              (item) =>
                (item.sync_state === "pending" ||
                  item.sync_state === "error") &&
                item.name.trim().toLocaleLowerCase() ===
                  record.name.trim().toLocaleLowerCase() &&
                item.subject.trim().toLocaleLowerCase() ===
                  (record.description ?? "General studies")
                    .trim()
                    .toLocaleLowerCase()
            )
          : undefined
      if (pendingMatch) {
        adoptedLocalIds.add(pendingMatch.id)
        if (selectedSnapshot(account) === pendingMatch.id)
          persistSelected(record.id, account)
      }
      return fromServer(record, exact ?? pendingMatch)
    })
    const merged = [
      ...mapped,
      ...local.filter(
        (item) =>
          !remoteIds.has(item.id) &&
          !adoptedLocalIds.has(item.id) &&
          (item.sync_state === "pending" || item.sync_state === "error")
      ),
    ]
    persistClassrooms(merged, account)
    return true
  } catch {
    return false
  }
}

export async function createClassroomOnServer(
  local: Classroom
): Promise<Classroom | null> {
  const account = accountScope()
  try {
    const remote = await api.createClassroom({
      name: local.name,
      description: local.subject,
      client_id: local.id,
    })
    if (accountScope() !== account) return null
    return replaceClassroom(
      local.id,
      fromServer(remote, local, local.members),
      account
    )
  } catch {
    if (accountScope() === account) {
      const current = classroomSnapshot(account)
      persistClassrooms(
        current.map((classroom) =>
          classroom.id === local.id
            ? { ...classroom, sync_state: "error" }
            : classroom
        ),
        account
      )
    }
    return null
  }
}

export async function joinClassroomOnServer(
  code: string,
  member: MemberInput
): Promise<Classroom | null> {
  const account = accountScope()
  try {
    const remote = await api.joinClassroom(code)
    if (accountScope() !== account) return null
    const existing =
      classroomSnapshot(account).find((item) => item.id === remote.id) ?? null
    const email = member.email?.trim().toLowerCase()
    const nextMember = memberFromInput({
      ...member,
      email,
      role: "student",
      status: "active",
    })
    const members = existing?.members ?? []
    const mergedMembers = mergeJoinedMember(members, nextMember, email)
    const next = fromServer(remote, existing ?? undefined, mergedMembers)
    persistClassrooms(
      [
        next,
        ...classroomSnapshot(account).filter((item) => item.id !== next.id),
      ],
      account
    )
    persistSelected(next.id, account)
    return next
  } catch {
    return null
  }
}

export async function syncClassroomRosterFromApi(
  classroomId: string
): Promise<Classroom | null> {
  const account = accountScope()
  try {
    const [rows, invitations] = await Promise.all([
      api.classroomRoster(classroomId),
      typeof api.classroomInvitations === "function"
        ? api.classroomInvitations(classroomId)
        : Promise.resolve([]),
    ])
    if (accountScope() !== account) return null
    const classroom =
      classroomSnapshot(account).find((item) => item.id === classroomId) ?? null
    if (!classroom) return null
    const next = {
      ...classroom,
      invitations,
      members: membersFromServer(rows, classroom, invitations),
    }
    persistClassrooms(
      classroomSnapshot(account).map((item) =>
        item.id === classroomId ? next : item
      ),
      account
    )
    return next
  } catch {
    return null
  }
}

export async function updateClassroomOnServer(
  classroomId: string,
  payload: { name?: string; description?: string }
): Promise<Classroom> {
  const account = accountScope()
  const classroom = classroomSnapshot(account).find(
    (item) => item.id === classroomId
  )
  if (!classroom) throw new Error("Classroom not found")
  const remote = await api.updateClassroom(classroomId, payload)
  if (accountScope() !== account)
    throw new Error(
      "The active account changed before the class update finished"
    )
  const next = fromServer(remote, classroom)
  persistClassrooms(
    classroomSnapshot(account).map((item) =>
      item.id === classroomId ? next : item
    ),
    account
  )
  return next
}

export async function deleteClassroomOnServer(
  classroomId: string
): Promise<void> {
  const account = accountScope()
  const remote = await api.listClassrooms()
  if (remote.some((item) => item.id === classroomId))
    await api.deleteClassroom(classroomId)
  if (accountScope() !== account) return
  const remaining = classroomSnapshot(account).filter(
    (item) => item.id !== classroomId
  )
  persistClassrooms(remaining, account)
  if (selectedSnapshot(account) === classroomId)
    persistSelected(remaining[0]?.id ?? null, account)
}

export async function leaveClassroomOnServer(
  classroomId: string
): Promise<void> {
  const account = accountScope()
  const remote = await api.listClassrooms()
  if (remote.some((item) => item.id === classroomId))
    await api.leaveClassroom(classroomId)
  if (accountScope() !== account) return
  const remaining = classroomSnapshot(account).filter(
    (item) => item.id !== classroomId
  )
  persistClassrooms(remaining, account)
  if (selectedSnapshot(account) === classroomId)
    persistSelected(remaining[0]?.id ?? null, account)
}

export async function inviteClassroomMemberOnServer(
  classroomId: string,
  input: { name: string; email: string }
): Promise<ClassroomInvitation> {
  const account = accountScope()
  const email = input.email.trim().toLocaleLowerCase()
  if (!email) throw new Error("Enter the learner's email address")
  const invitationRows =
    typeof api.classroomInvitations === "function"
      ? await api.classroomInvitations(classroomId)
      : []
  let invitation = invitationRows.find(
    (item) =>
      item.email.trim().toLocaleLowerCase() === email &&
      item.status === "pending"
  )
  if (!invitation)
    invitation = await api.inviteClassroomMember(classroomId, {
      name: input.name.trim(),
      email,
    })
  if (accountScope() !== account)
    throw new Error("The active account changed before the invitation finished")
  const classroom = classroomSnapshot(account).find(
    (item) => item.id === classroomId
  )
  if (classroom) {
    const allInvitations = [
      ...invitationRows.filter(
        (item) => item.email.trim().toLocaleLowerCase() !== email
      ),
      invitation,
    ]
    const members = classroom.members.filter(
      (member) =>
        !(
          member.status !== "active" &&
          member.email.toLocaleLowerCase() === email
        )
    )
    const next = {
      ...classroom,
      invitations: allInvitations,
      members:
        invitation.status === "pending"
          ? [
              ...members,
              {
                id: invitation.id,
                name: invitation.name,
                email: invitation.email,
                role: "student" as const,
                status: "pending" as const,
                joined_at: invitation.created_at,
              },
            ]
          : members,
    }
    persistClassrooms(
      classroomSnapshot(account).map((item) =>
        item.id === classroomId ? next : item
      ),
      account
    )
  }
  return invitation
}

export async function removeClassroomInvitationOnServer(
  classroomId: string,
  invitationId: string
): Promise<void> {
  const account = accountScope()
  const rows =
    typeof api.classroomInvitations === "function"
      ? await api.classroomInvitations(classroomId)
      : []
  if (
    rows.some((item) => item.id === invitationId && item.status === "pending")
  ) {
    await api.deleteClassroomInvitation(classroomId, invitationId)
  }
  if (accountScope() !== account) return
  const classroom = classroomSnapshot(account).find(
    (item) => item.id === classroomId
  )
  if (!classroom) return
  const invitations = (classroom.invitations ?? []).filter(
    (item) => item.id !== invitationId
  )
  const members = classroom.members.filter(
    (member) => member.id !== invitationId
  )
  const next = { ...classroom, invitations, members }
  persistClassrooms(
    classroomSnapshot(account).map((item) =>
      item.id === classroomId ? next : item
    ),
    account
  )
}

export async function removeClassroomMemberOnServer(
  classroomId: string,
  memberId: string
): Promise<boolean> {
  const account = accountScope()
  try {
    const rows = await api.classroomRoster(classroomId)
    if (rows.some((member) => member.user_id === memberId)) {
      await api.removeClassroomMember(classroomId, memberId)
    }
    if (accountScope() === account) {
      const classroom = classroomSnapshot(account).find(
        (item) => item.id === classroomId
      )
      if (classroom) {
        const next = {
          ...classroom,
          members: classroom.members.filter((member) => member.id !== memberId),
        }
        persistClassrooms(
          classroomSnapshot(account).map((item) =>
            item.id === classroomId ? next : item
          ),
          account
        )
      }
    }
    return true
  } catch {
    return false
  }
}

function memberFromInput(
  input: MemberInput,
  fallbackRole: Role = "student"
): ClassroomMember {
  return {
    id: input.id ?? randomId("member"),
    name: input.name.trim(),
    email: input.email?.trim() ?? "",
    role: input.role ?? fallbackRole,
    status: input.status ?? "active",
    joined_at: nowIso(),
  }
}

export function newClassJoinCode(): string {
  const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
  const bytes = new Uint8Array(6)
  if (
    typeof crypto !== "undefined" &&
    typeof crypto.getRandomValues === "function"
  ) {
    crypto.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 255)
    }
  }
  return Array.from(bytes, (byte) => alphabet[byte % alphabet.length]).join("")
}

export function createClassroom(input: {
  name: string
  subject?: string
  owner: MemberInput
}): Classroom {
  const createdAt = nowIso()
  const classroom: Classroom = {
    id: randomId("class"),
    name: input.name.trim(),
    subject: input.subject?.trim() || "General studies",
    join_code: "",
    created_at: createdAt,
    members: [
      memberFromInput(
        { ...input.owner, role: "teacher", status: "active" },
        "teacher"
      ),
    ],
    sync_state: "pending",
  }
  persistClassrooms([classroom, ...getClassroomSnapshot()])
  persistSelected(classroom.id)
  return classroom
}

export function addClassroomMember(
  classroomId: string,
  input: MemberInput
): Classroom | null {
  const classroom = getClassroom(classroomId)
  if (!classroom) return null

  const email = input.email?.trim().toLowerCase()
  const duplicate = classroom.members.some(
    (member) =>
      (email && member.email.toLowerCase() === email) ||
      (!email && member.name.toLowerCase() === input.name.trim().toLowerCase())
  )
  if (duplicate) return classroom

  const updated: Classroom = {
    ...classroom,
    members: [...classroom.members, memberFromInput({ ...input, email })],
  }
  persistClassrooms(
    getClassroomSnapshot().map((item) =>
      item.id === classroomId ? updated : item
    )
  )
  return updated
}

export function joinClassroom(
  code: string,
  member: MemberInput
): Classroom | null {
  const normalised = code.trim().toUpperCase()
  const classroom = getClassroomSnapshot().find(
    (item) => item.join_code === normalised
  )
  if (!classroom) return null

  const email = member.email?.trim().toLowerCase()
  const existing = classroom.members.find(
    (item) =>
      (email && item.email.toLowerCase() === email) || item.id === member.id
  )
  const updated = existing
    ? {
        ...classroom,
        members: classroom.members.map((item) =>
          item.id === existing.id
            ? { ...item, status: "active" as const }
            : item
        ),
      }
    : {
        ...classroom,
        members: [
          ...classroom.members,
          memberFromInput({ ...member, email, role: "student" }),
        ],
      }

  persistClassrooms(
    getClassroomSnapshot().map((item) =>
      item.id === classroom.id ? updated : item
    )
  )
  persistSelected(classroom.id)
  return updated
}

export function removeClassroomMember(
  classroomId: string,
  memberId: string
): Classroom | null {
  const classroom = getClassroom(classroomId)
  if (!classroom || classroom.members.length <= 1) return classroom
  const owner = classroom.members.find((member) => member.role === "teacher")
  if (owner?.id === memberId) return classroom

  const updated = {
    ...classroom,
    members: classroom.members.filter((member) => member.id !== memberId),
  }
  persistClassrooms(
    getClassroomSnapshot().map((item) =>
      item.id === classroomId ? updated : item
    )
  )
  return updated
}

export function setClassroomActiveRoom(
  classroomId: string,
  roomId: string | undefined
): void {
  const updated = getClassroomSnapshot().map((classroom) =>
    classroom.id === classroomId
      ? { ...classroom, active_room_id: roomId }
      : classroom
  )
  persistClassrooms(updated)
}

export function selectClassroom(classroomId: string | null): void {
  const exists = classroomId
    ? getClassroomSnapshot().some((item) => item.id === classroomId)
    : true
  persistSelected(exists ? classroomId : null)
}

export function getClassroom(classroomId: string): Classroom | null {
  return (
    getClassroomSnapshot().find((classroom) => classroom.id === classroomId) ??
    null
  )
}

/** Imperative selection read for non-React consumers such as hydration helpers. */
export function getSelectedClassroomId(): string | null {
  return getSelectedSnapshot()
}

export function useClassrooms(): Classroom[] {
  return useSyncExternalStore(
    subscribe,
    getClassroomSnapshot,
    getServerClassrooms
  )
}

export function useSelectedClassroom(): Classroom | null {
  const classrooms = useClassrooms()
  const selectedId = useSyncExternalStore(
    subscribe,
    getSelectedSnapshot,
    getServerSelected
  )
  const selected = classrooms.find((classroom) => classroom.id === selectedId)
  if (selected) return selected
  return classrooms.length > 0 ? classrooms[0] : null
}
